package httpapi

import (
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

Create still flows through POST /api/resources (handleApply), which already
accepts a mixed batch. {kind} uses the same vocabulary as dependency kinds:
infrastructure | application | agent | memory_store.
*/

// validKind reports whether k is an addressable resource kind.
func validKind(k string) bool {
	switch model.DepKind(k) {
	case model.DepInfrastructure, model.DepApplication, model.DepAgent, model.DepMemoryStore:
		return true
	default:
		return false
	}
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

// handleUpdateResource edits a definition, dispatching to the kind's own decode
// + validation. Ownership is enforced by the per-kind write-permission helpers.
func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	kind := chi.URLParam(r, "kind")
	rid := chi.URLParam(r, "id")
	if !validKind(kind) {
		writeErr(w, http.StatusNotFound, "unknown resource kind")
		return
	}

	switch model.DepKind(kind) {
	case model.DepInfrastructure:
		owner, ok := s.infraWriteAllowed(w, r, id, rid)
		if !ok {
			return
		}
		var body struct {
			Name         string             `json:"name"`
			Description  string             `json:"description"`
			BicepModule  string             `json:"bicepModule"`
			BicepParams  map[string]any     `json:"bicepParams"`
			Dependencies []model.Dependency `json:"dependencies"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeErr(w, http.StatusBadRequest, "name is required")
			return
		}
		if strings.TrimSpace(body.BicepModule) == "" {
			writeErr(w, http.StatusBadRequest, "a Bicep module reference is required")
			return
		}
		upd := model.Infrastructure{
			ID:           rid,
			Name:         strings.TrimSpace(body.Name),
			Description:  strings.TrimSpace(body.Description),
			Owner:        owner,
			BicepModule:  strings.TrimSpace(body.BicepModule),
			BicepParams:  body.BicepParams,
			Dependencies: body.Dependencies,
		}
		if !s.validateDeps(w, r, model.DepInfrastructure, rid, owner, upd.Dependencies) {
			return
		}
		if !s.resolveInfra(w, r, &upd) {
			return
		}
		if s.mapStoreErr(w, r, s.store.UpdateInfrastructure(r.Context(), upd)) {
			return
		}

	case model.DepApplication:
		if !s.appWriteAllowed(w, r, id, rid) {
			return
		}
		existing, err := s.store.ApplicationByID(r.Context(), rid)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var body struct {
			Name           string             `json:"name"`
			Description    string             `json:"description"`
			Namespace      string             `json:"namespace"`
			RepoURL        string             `json:"repoURL"`
			Chart          string             `json:"chart"`
			TargetRevision string             `json:"targetRevision"`
			Values         string             `json:"values"`
			Wiring         []shared.WireLink  `json:"wiring"`
			Dependencies   []model.Dependency `json:"dependencies"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeErr(w, http.StatusBadRequest, "name is required")
			return
		}
		if strings.TrimSpace(body.RepoURL) == "" || strings.TrimSpace(body.Chart) == "" {
			writeErr(w, http.StatusBadRequest, "repoURL and chart are required")
			return
		}
		ns := strings.TrimSpace(body.Namespace)
		if ns == "" {
			ns = "default"
		}
		upd := model.Application{
			ID:             rid,
			Name:           strings.TrimSpace(body.Name),
			Description:    strings.TrimSpace(body.Description),
			Namespace:      ns,
			RepoURL:        strings.TrimSpace(body.RepoURL),
			Chart:          strings.TrimSpace(body.Chart),
			TargetRevision: strings.TrimSpace(body.TargetRevision),
			Values:         body.Values,
			Wiring:         body.Wiring,
			Dependencies:   body.Dependencies,
		}
		if !s.validateDeps(w, r, model.DepApplication, rid, existing.Owner, upd.Dependencies) {
			return
		}
		if s.mapStoreErr(w, r, s.store.UpdateApplication(r.Context(), upd)) {
			return
		}

	case model.DepAgent:
		if !s.catalogWriteAllowed(w, r, id, rid) {
			return
		}
		var body struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Model       string                 `json:"model"`
			Definition  shared.AgentDefinition `json:"definition"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeErr(w, http.StatusBadRequest, "name is required")
			return
		}
		if s.mapStoreErr(w, r, s.store.UpdateCatalogAgent(r.Context(), rid,
			strings.TrimSpace(body.Name), strings.TrimSpace(body.Description),
			strings.TrimSpace(body.Model), body.Definition)) {
			return
		}

	case model.DepMemoryStore:
		if !s.storeWriteAllowed(w, r, id, rid) {
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeErr(w, http.StatusBadRequest, "name is required")
			return
		}
		if s.mapStoreErr(w, r, s.store.UpdateMemoryStore(r.Context(), rid,
			strings.TrimSpace(body.Name), strings.TrimSpace(body.Description))) {
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleDeleteResource removes a definition. Deletion is intentionally not
// blocked by in-use for authored definitions — the admin is choosing to remove
// them; the per-kind store methods clean up entitlements and enabled instances.
func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	kind := chi.URLParam(r, "kind")
	rid := chi.URLParam(r, "id")
	if !validKind(kind) {
		writeErr(w, http.StatusNotFound, "unknown resource kind")
		return
	}

	var err error
	switch model.DepKind(kind) {
	case model.DepInfrastructure:
		if _, ok := s.infraWriteAllowed(w, r, id, rid); !ok {
			return
		}
		err = s.store.DeleteInfrastructure(r.Context(), rid)
	case model.DepApplication:
		if !s.appWriteAllowed(w, r, id, rid) {
			return
		}
		err = s.store.DeleteApplication(r.Context(), rid)
	case model.DepAgent:
		if !s.catalogWriteAllowed(w, r, id, rid) {
			return
		}
		err = s.store.DeleteCatalogAgent(r.Context(), rid)
	case model.DepMemoryStore:
		if !s.storeWriteAllowed(w, r, id, rid) {
			return
		}
		err = s.store.DeleteMemoryStore(r.Context(), rid)
	}
	if s.mapStoreErr(w, r, err) {
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
	kind := chi.URLParam(r, "kind")
	rid := chi.URLParam(r, "id")
	if !validKind(kind) {
		writeErr(w, http.StatusNotFound, "unknown resource kind")
		return
	}

	var err error
	switch model.DepKind(kind) {
	case model.DepInfrastructure:
		err = s.store.EnableInfrastructure(r.Context(), t.ID, rid)
	case model.DepApplication:
		err = s.store.EnableDeployment(r.Context(), t.ID, rid)
	case model.DepAgent:
		var body struct {
			PublishTo []string `json:"publishTo"`
		}
		// Body is optional for enable; ignore decode failure on empty body.
		_ = decodeJSONOptional(r, &body)
		err = s.store.EnableAgent(r.Context(), t.ID, rid, body.PublishTo)
	case model.DepMemoryStore:
		err = s.store.EnableStore(r.Context(), t.ID, rid)
	}
	if s.mapStoreErr(w, r, err) {
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
	kind := chi.URLParam(r, "kind")
	rid := chi.URLParam(r, "id")
	if !validKind(kind) {
		writeErr(w, http.StatusNotFound, "unknown resource kind")
		return
	}

	var err error
	switch model.DepKind(kind) {
	case model.DepInfrastructure:
		err = s.store.DisableInfrastructure(r.Context(), t.ID, rid)
	case model.DepApplication:
		err = s.store.DisableDeployment(r.Context(), t.ID, rid)
	case model.DepAgent:
		err = s.store.DisableAgent(r.Context(), t.ID, rid)
	case model.DepMemoryStore:
		err = s.store.DisableStore(r.Context(), t.ID, rid)
	}
	if s.mapStoreErr(w, r, err) {
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
	if body.Infrastructure == nil {
		body.Infrastructure = []string{}
	}
	if body.Applications == nil {
		body.Applications = []string{}
	}
	if body.Agents == nil {
		body.Agents = []string{}
	}
	if body.MemoryStores == nil {
		body.MemoryStores = []string{}
	}

	if s.mapStoreErr(w, r, s.store.SetInfrastructureEntitlements(r.Context(), slug, body.Infrastructure)) {
		return
	}
	if s.mapStoreErr(w, r, s.store.SetDeploymentEntitlements(r.Context(), slug, body.Applications)) {
		return
	}
	if s.mapStoreErr(w, r, s.store.SetEntitlements(r.Context(), slug, body.Agents)) {
		return
	}
	if s.mapStoreErr(w, r, s.store.SetStoreEntitlements(r.Context(), slug, body.MemoryStores)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
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
