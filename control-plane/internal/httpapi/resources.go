package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/inception42/cortex/control-plane/internal/auth"
	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/control-plane/internal/store"
	"github.com/inception42/cortex/shared"
)

/*
Generic resource surface.

Every catalog entity — infrastructure, application, agent, memory_store — is
addressed through one uniform set of endpoints instead of four bespoke ones:

	GET    /api/resources                       list every kind (role-aware)
	PATCH  /api/resources/{kind}/{id}           edit a definition
	DELETE /api/resources/{kind}/{id}           remove a definition
	POST   /api/resources/{kind}/{id}/enable    turn on in the caller's tenant
	DELETE /api/resources/{kind}/{id}/enable    turn off in the caller's tenant
	PATCH  /api/tenants/{slug}/all-entitlements grant/revoke every kind at once

Create flows through POST /api/resources (handleApply), a mixed batch. The
per-kind differences live in exactly two places: the input types below (decode +
validate + build, shared by create and update) and the resourceOps registry
(the store calls each verb dispatches to). Everything else — auth, error
mapping, JSON envelopes — is written once in the generic handlers.
*/

/* ── Per-kind inputs: decode + validate + build, shared by create & update ── */

type infraInput struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	BicepModule  string             `json:"bicepModule"`
	BicepParams  map[string]any     `json:"bicepParams"`
	Dependencies []model.Dependency `json:"dependencies"`
}

// validate returns a human error message, or "" when the input is well-formed.
func (in infraInput) validate() string {
	if slugify(in.Name) == "" {
		return "name is required"
	}
	if strings.TrimSpace(in.BicepModule) == "" {
		return "a Bicep module reference is required"
	}
	return ""
}

func (in infraInput) build(id, owner string) model.Infrastructure {
	return model.Infrastructure{
		ID:           id,
		Name:         strings.TrimSpace(in.Name),
		Description:  strings.TrimSpace(in.Description),
		Owner:        owner,
		BicepModule:  strings.TrimSpace(in.BicepModule),
		BicepParams:  in.BicepParams,
		Dependencies: in.Dependencies,
	}
}

type appInput struct {
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Namespace      string             `json:"namespace"`
	RepoURL        string             `json:"repoURL"`
	Chart          string             `json:"chart"`
	TargetRevision string             `json:"targetRevision"`
	Values         string             `json:"values"`
	ExposeService  string             `json:"exposeService"`
	ExposePort     int                `json:"exposePort"`
	Wiring         []shared.WireLink  `json:"wiring"`
	Dependencies   []model.Dependency `json:"dependencies"`
}

func (in appInput) validate() string {
	if slugify(in.Name) == "" {
		return "name is required"
	}
	if strings.TrimSpace(in.RepoURL) == "" || strings.TrimSpace(in.Chart) == "" {
		return "repoURL and chart are required"
	}
	return ""
}

func (in appInput) build(id, owner string) model.Application {
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		ns = "default"
	}
	port := in.ExposePort
	if port == 0 {
		port = 80
	}
	return model.Application{
		ID:             id,
		Name:           strings.TrimSpace(in.Name),
		Description:    strings.TrimSpace(in.Description),
		Owner:          owner,
		Namespace:      ns,
		RepoURL:        strings.TrimSpace(in.RepoURL),
		Chart:          strings.TrimSpace(in.Chart),
		TargetRevision: strings.TrimSpace(in.TargetRevision),
		Values:         in.Values,
		ExposeService:  strings.TrimSpace(in.ExposeService),
		ExposePort:     port,
		Wiring:         in.Wiring,
		Dependencies:   in.Dependencies,
	}
}

type agentInput struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"`
	Model       string                 `json:"model"`
	Definition  shared.AgentDefinition `json:"definition"`
}

func (in agentInput) validate() string {
	if slugify(in.Name) == "" {
		return "name is required"
	}
	return ""
}

func (in agentInput) name() string { return strings.TrimSpace(in.Name) }
func (in agentInput) desc() string { return strings.TrimSpace(in.Description) }

// normModel defaults to Foundry's standard chat model so an agent is always
// provisionable.
func (in agentInput) normModel() string {
	if m := strings.TrimSpace(in.Model); m != "" {
		return m
	}
	return "gpt-4o"
}

func (in agentInput) normType() string {
	if in.Type == "hosted" {
		return "hosted"
	}
	return "prompt"
}

func (in agentInput) applyAgent(id, owner string) store.ApplyAgent {
	return store.ApplyAgent{
		ID: id, Name: in.name(), Description: in.desc(),
		Type: in.normType(), Model: in.normModel(), Owner: owner, Definition: in.Definition,
	}
}

type storeInput struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Definition  shared.MemoryStoreDefinition `json:"definition"`
}

func (in storeInput) validate() string {
	if slugify(in.Name) == "" {
		return "name is required"
	}
	return ""
}

func (in storeInput) build(id, owner string) model.MemoryStore {
	return model.MemoryStore{
		ID: id, Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description),
		Owner: owner, Definition: in.Definition,
	}
}

/* ── Per-kind registry: the store calls each generic verb dispatches to ────── */

// resourceOps captures everything that genuinely differs per kind. The generic
// handlers own auth, error mapping, and JSON; these closures own only the decode
// (for update) and the store calls. Package-level, so each takes *Server.
type resourceOps struct {
	// writeAllowed authorises editing/removing definition rid, returning its
	// owner tenant ("" = platform). It writes the response on denial.
	writeAllowed func(s *Server, w http.ResponseWriter, r *http.Request, id model.Identity, rid string) (owner string, ok bool)
	// update decodes the request body and applies an in-place edit. proceed is
	// false when it already wrote a 4xx (bad body); otherwise err is the store
	// result for the generic handler to map.
	update func(s *Server, w http.ResponseWriter, r *http.Request, rid, owner string) (proceed bool, err error)
	// remove deletes the definition.
	remove func(s *Server, ctx context.Context, rid string) error
	// enable / disable toggle the definition in tenant slug (desired state).
	enable  func(s *Server, w http.ResponseWriter, r *http.Request, slug, rid string) error
	disable func(s *Server, ctx context.Context, slug, rid string) error
}

var resourceOpsByKind = map[model.DepKind]resourceOps{
	model.DepInfrastructure: {
		writeAllowed: (*Server).infraWriteAllowed,
		update: func(s *Server, w http.ResponseWriter, r *http.Request, rid, owner string) (bool, error) {
			in, ok := decodeValid[infraInput](w, r)
			if !ok {
				return false, nil
			}
			upd := in.build(rid, owner)
			if !s.validateDeps(w, r, model.DepInfrastructure, rid, owner, upd.Dependencies) {
				return false, nil
			}
			if !s.resolveInfra(w, r, &upd) {
				return false, nil
			}
			return true, s.store.UpdateInfrastructure(r.Context(), upd)
		},
		remove: func(s *Server, ctx context.Context, rid string) error { return s.store.DeleteInfrastructure(ctx, rid) },
		enable: func(s *Server, w http.ResponseWriter, r *http.Request, slug, rid string) error {
			return s.store.EnableInfrastructure(r.Context(), slug, rid)
		},
		disable: func(s *Server, ctx context.Context, slug, rid string) error {
			return s.store.DisableInfrastructure(ctx, slug, rid)
		},
	},

	model.DepApplication: {
		writeAllowed: (*Server).appWriteAllowed,
		update: func(s *Server, w http.ResponseWriter, r *http.Request, rid, owner string) (bool, error) {
			in, ok := decodeValid[appInput](w, r)
			if !ok {
				return false, nil
			}
			upd := in.build(rid, owner)
			if !s.validateDeps(w, r, model.DepApplication, rid, owner, upd.Dependencies) {
				return false, nil
			}
			return true, s.store.UpdateApplication(r.Context(), upd)
		},
		remove: func(s *Server, ctx context.Context, rid string) error { return s.store.DeleteApplication(ctx, rid) },
		enable: func(s *Server, w http.ResponseWriter, r *http.Request, slug, rid string) error {
			return s.store.EnableDeployment(r.Context(), slug, rid)
		},
		disable: func(s *Server, ctx context.Context, slug, rid string) error {
			return s.store.DisableDeployment(ctx, slug, rid)
		},
	},

	model.DepAgent: {
		writeAllowed: (*Server).catalogWriteAllowed,
		update: func(s *Server, w http.ResponseWriter, r *http.Request, rid, owner string) (bool, error) {
			in, ok := decodeValid[agentInput](w, r)
			if !ok {
				return false, nil
			}
			return true, s.store.UpdateCatalogAgent(r.Context(), rid, in.name(), in.desc(), in.normModel(), in.Definition)
		},
		remove: func(s *Server, ctx context.Context, rid string) error { return s.store.DeleteCatalogAgent(ctx, rid) },
		enable: func(s *Server, w http.ResponseWriter, r *http.Request, slug, rid string) error {
			var body struct {
				PublishTo []string `json:"publishTo"`
			}
			_ = decodeJSONOptional(r, &body) // body is optional for enable
			return s.store.EnableAgent(r.Context(), slug, rid, body.PublishTo)
		},
		disable: func(s *Server, ctx context.Context, slug, rid string) error {
			return s.store.DisableAgent(ctx, slug, rid)
		},
	},

	model.DepMemoryStore: {
		writeAllowed: (*Server).storeWriteAllowed,
		update: func(s *Server, w http.ResponseWriter, r *http.Request, rid, owner string) (bool, error) {
			in, ok := decodeValid[storeInput](w, r)
			if !ok {
				return false, nil
			}
			// A store's definition is immutable (Foundry has no update surface),
			// so only name + description are editable.
			return true, s.store.UpdateMemoryStore(r.Context(), rid, strings.TrimSpace(in.Name), strings.TrimSpace(in.Description))
		},
		remove: func(s *Server, ctx context.Context, rid string) error { return s.store.DeleteMemoryStore(ctx, rid) },
		enable: func(s *Server, w http.ResponseWriter, r *http.Request, slug, rid string) error {
			return s.store.EnableStore(r.Context(), slug, rid)
		},
		disable: func(s *Server, ctx context.Context, slug, rid string) error {
			return s.store.DisableStore(ctx, slug, rid)
		},
	},
}

// opsFor looks up the registry entry for a URL {kind}, writing a 404 when the
// kind is unknown.
func opsFor(w http.ResponseWriter, r *http.Request) (resourceOps, string, bool) {
	ops, ok := resourceOpsByKind[model.DepKind(chi.URLParam(r, "kind"))]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown resource kind")
		return resourceOps{}, "", false
	}
	return ops, chi.URLParam(r, "id"), true
}

// mapStoreErr translates a store error into an HTTP response and reports whether
// it handled one. It centralises the error vocabulary shared by every kind so
// the generic handlers don't each re-spell the same switch.
func (s *Server) mapStoreErr(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrNotEntitled),
		errors.Is(err, store.ErrStoreNotAccessible),
		errors.Is(err, store.ErrDeploymentNotAccessible),
		errors.Is(err, store.ErrInfrastructureNotAccessible):
		writeErr(w, http.StatusForbidden, "not available to your tenant")
	case errors.Is(err, store.ErrInUse),
		errors.Is(err, store.ErrStoreInUse),
		errors.Is(err, store.ErrEntitlementInUse):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		s.fail(w, r, err)
	}
	return true
}

// handleListResources returns every catalog entity the caller can see in one
// payload: the platform sees the whole catalog, a tenant sees what it authored.
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	ctx := r.Context()

	var (
		infra  []model.Infrastructure
		apps   []model.Application
		agents []model.CatalogAgent
		stores []model.MemoryStore
		err    error
	)

	if id.Role == model.RolePlatform {
		if infra, err = s.store.InfrastructureList(ctx); err != nil {
			s.fail(w, r, err)
			return
		}
		if apps, err = s.store.ApplicationList(ctx); err != nil {
			s.fail(w, r, err)
			return
		}
		if agents, err = s.store.CatalogList(ctx); err != nil {
			s.fail(w, r, err)
			return
		}
		if stores, err = s.store.MemoryStoreList(ctx); err != nil {
			s.fail(w, r, err)
			return
		}
	} else {
		t, ok := s.callerTenant(w, r)
		if !ok {
			return
		}
		if infra, err = s.store.InfrastructureForTenant(ctx, t.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		if apps, err = s.store.ApplicationsForTenant(ctx, t.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		if agents, err = s.store.CatalogForTenant(ctx, t.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		if stores, err = s.store.MemoryStoresForTenant(ctx, t.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"infrastructure": infra,
		"applications":   apps,
		"agents":         agents,
		"memoryStores":   stores,
	})
}

// handleUpdateResource edits a definition. The skeleton (auth → decode+apply →
// error map → envelope) is shared; the kind's registry entry supplies the decode
// and the store call.
func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	ops, rid, ok := opsFor(w, r)
	if !ok {
		return
	}
	owner, ok := ops.writeAllowed(s, w, r, id, rid)
	if !ok {
		return
	}
	proceed, err := ops.update(s, w, r, rid, owner)
	if !proceed {
		return
	}
	if s.mapStoreErr(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleDeleteResource removes a definition. Deletion is intentionally not
// blocked by in-use for authored definitions — the admin is choosing to remove
// them; the per-kind store methods clean up entitlements and enabled instances.
func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	ops, rid, ok := opsFor(w, r)
	if !ok {
		return
	}
	if _, ok := ops.writeAllowed(s, w, r, id, rid); !ok {
		return
	}
	if s.mapStoreErr(w, r, ops.remove(s, r.Context(), rid)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleEnableResource turns a resource on in the caller's tenant (desired
// state); the reconciler then converges it. Agents accept an optional publishTo.
func (s *Server) handleEnableResource(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	ops, rid, ok := opsFor(w, r)
	if !ok {
		return
	}
	if s.mapStoreErr(w, r, ops.enable(s, w, r, t.ID, rid)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
}

// handleDisableResource turns a resource off in the caller's tenant. In-use
// guards surface as 409 via mapStoreErr.
func (s *Server) handleDisableResource(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	ops, rid, ok := opsFor(w, r)
	if !ok {
		return
	}
	if s.mapStoreErr(w, r, ops.disable(s, r.Context(), t.ID, rid)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// handleSetAllEntitlements grants/revokes every kind for a tenant in one call,
// matching the consolidated entitlements panel. Each kind is applied in turn;
// an in-use revoke surfaces as 409 and leaves earlier kinds applied (the panel
// always sends the full desired state, so a retry converges).
func (s *Server) handleSetAllEntitlements(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	slug := chi.URLParam(r, "slug")
	var body struct {
		Infrastructure []string `json:"infrastructure"`
		Applications   []string `json:"applications"`
		Agents         []string `json:"agents"`
		MemoryStores   []string `json:"memoryStores"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	// slug → each store setter, applied in order. nilToEmpty makes "omitted"
	// mean "revoke all of that kind" rather than "leave unchanged".
	for _, set := range []func() error{
		func() error {
			return s.store.SetInfrastructureEntitlements(r.Context(), slug, nilToEmpty(body.Infrastructure))
		},
		func() error {
			return s.store.SetDeploymentEntitlements(r.Context(), slug, nilToEmpty(body.Applications))
		},
		func() error { return s.store.SetEntitlements(r.Context(), slug, nilToEmpty(body.Agents)) },
		func() error { return s.store.SetStoreEntitlements(r.Context(), slug, nilToEmpty(body.MemoryStores)) },
	} {
		if s.mapStoreErr(w, r, set()) {
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func nilToEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// decodeValid decodes a JSON body into a T and runs its validate(). It writes
// the 4xx itself (malformed JSON, or a validation message) and returns ok=false,
// so every update closure shares one decode+validate line.
func decodeValid[T interface{ validate() string }](w http.ResponseWriter, r *http.Request) (T, bool) {
	var in T
	if !decodeJSON(w, r, &in) {
		return in, false
	}
	if msg := in.validate(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return in, false
	}
	return in, true
}

// decodeJSONOptional decodes a JSON body into v, tolerating an empty body so a
// handler can treat the body as optional (e.g. enable with no publishTo).
// Malformed JSON is still surfaced as an error.
func decodeJSONOptional(r *http.Request, v any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
