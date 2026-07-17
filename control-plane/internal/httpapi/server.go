package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/inception42/cortex/control-plane/internal/auth"
	"github.com/inception42/cortex/control-plane/internal/bicep"
	"github.com/inception42/cortex/control-plane/internal/chart"
	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/control-plane/internal/store"
	"github.com/inception42/cortex/shared"
)

// resolveInfra resolves an infrastructure entity's OCI Bicep-module reference
// into a deployable ARM template + its output names (stored for the worker + the
// wiring UI). A bad reference or invalid module is a 400; a missing toolchain
// degrades gracefully — the definition still saves, infra is just unresolved.
func (s *Server) resolveInfra(w http.ResponseWriter, r *http.Request, i *model.Infrastructure) bool {
	arm, outputs, err := bicep.Resolve(r.Context(), i.BicepModule, i.BicepParams)
	if err != nil {
		if errors.Is(err, bicep.ErrNoCompiler) {
			i.ArmTemplate, i.BicepOutputs = "", nil
			return true
		}
		writeErr(w, http.StatusBadRequest, "bicep module: "+err.Error())
		return false
	}
	i.ArmTemplate, i.BicepOutputs = arm, outputs
	return true
}

// validateDeps runs author-time dependency validation (allowed edge, existing +
// accessible target, no cycle) and maps a bad graph to a 400. Returns true when
// the dependencies are valid (or absent).
func (s *Server) validateDeps(w http.ResponseWriter, r *http.Request, kind model.DepKind, id, owner string, deps []model.Dependency) bool {
	if err := s.store.ValidateDependencies(r.Context(), kind, id, owner, deps); err != nil {
		if errors.Is(err, store.ErrBadDependency) || errors.Is(err, store.ErrDependencyCycle) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return false
		}
		s.fail(w, r, err)
		return false
	}
	return true
}

type Server struct {
	store           *store.Store
	auth            *auth.Authenticator
	recon           *auth.ReconAuthenticator
	corsOrigin      string
	entraClientID   string // Cortex app registration — the audience clusters pin to
	entraIssuerHost string // Entra issuer host (cloud-specific), for per-tenant issuers
}

func NewServer(st *store.Store, a *auth.Authenticator, recon *auth.ReconAuthenticator, corsOrigin, entraClientID, entraIssuerHost string) *Server {
	return &Server{
		store:           st,
		auth:            a,
		recon:           recon,
		corsOrigin:      corsOrigin,
		entraClientID:   entraClientID,
		entraIssuerHost: entraIssuerHost,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(s.cors)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(s.auth.Middleware)
		// /me is intentionally NOT tenant-gated: a pending (not-yet-enabled)
		// tenant must still be able to learn its own status so the console can
		// show a "pending approval" screen instead of failing opaquely.
		r.Get("/me", s.handleMe)

		// Everything else requires an ENABLED tenant. Platform admins always pass.
		r.Group(func(r chi.Router) {
			r.Use(s.tenantGate)
			r.Get("/fleet", s.handleFleet)
			r.Get("/tenant/context", s.handleMyContext)
			r.Get("/tenants/{slug}/context", s.handleTenantContext)

			// Unified transactional create — any combination of infrastructure,
			// memory stores, agents, and applications in one request.
			r.Post("/resources", s.handleApply)

			// Generic resource surface — list/edit/remove/enable/disable any kind
			// (infrastructure | application | agent | memory_store) uniformly.
			r.Get("/resources", s.handleListResources)
			r.Patch("/resources/{kind}/{id}", s.handleUpdateResource)
			r.Delete("/resources/{kind}/{id}", s.handleDeleteResource)
			r.Post("/resources/{kind}/{id}/enable", s.handleEnableResource)
			r.Delete("/resources/{kind}/{id}/enable", s.handleDisableResource)

			// Authoring inspectors (typed forms for Bicep modules + Helm charts).
			r.Post("/infrastructure/inspect", s.handleInspectModule)
			r.Post("/applications/inspect", s.handleInspectModule)
			r.Post("/applications/inspect-chart", s.handleInspectChart)

			// Tenant registry + entitlements + access (platform)
			r.Get("/tenants", s.handleTenantsRegistry)
			r.Patch("/tenants/{slug}/all-entitlements", s.handleSetAllEntitlements)
			r.Patch("/tenants/{slug}/enabled", s.handleSetTenantEnabled)

			// Agent ↔ memory-store connection (a relation, not a CRUD verb).
			r.Post("/tenant/agents/{agentId}/store", s.handleConnectAgentStore)
		})
	})

	// Reconciler-facing endpoints. The in-tenant reconciler authenticates with
	// its own Entra token (managed identity in Azure; dev secret locally); the
	// tenant it acts on is the token's tid — never a client-supplied parameter.
	r.Route("/recon", func(r chi.Router) {
		r.Use(s.recon.Middleware)
		r.Use(s.reconGate)
		r.Get("/sync", s.handleSync)
		r.Post("/heartbeat", s.handleHeartbeat)
	})
	return r
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	resp := model.MeResponse{Identity: id}

	if id.Role == model.RoleTenant {
		t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		resp.Tenant = &t
		if err := s.store.UpsertUser(r.Context(), id, &t.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	} else if err := s.store.UpsertUser(r.Context(), id, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// tenantGate blocks non-enabled tenants from every /api route except /me.
// Platform admins always pass. First contact records the tenant (disabled) so it
// surfaces for platform approval, then rejects it so the console can show a
// pending-approval screen.
func (s *Server) tenantGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		if id.Role == model.RolePlatform {
			next.ServeHTTP(w, r) // the platform tenant is always enabled
			return
		}
		t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !t.Enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant not enabled", "code": "tenant_disabled"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// reconGate blocks a reconciler whose tenant isn't enabled. An unknown tenant is
// recorded (disabled) so it surfaces for approval, then rejected.
func (s *Server) reconGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.ReconIdentityFrom(r.Context())
		if !ok || id.TID == "" {
			writeErr(w, http.StatusUnauthorized, "unauthenticated reconciler")
			return
		}
		t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, "")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !t.Enabled {
			writeErr(w, http.StatusForbidden, "tenant not enabled")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleSetTenantEnabled enables or disables a tenant's access (platform only).
func (s *Server) handleSetTenantEnabled(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.store.SetTenantEnabled(r.Context(), chi.URLParam(r, "slug"), body.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if id.Role != model.RolePlatform {
		writeErr(w, http.StatusForbidden, "platform admins only")
		return
	}
	fleet, err := s.store.Fleet(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fleet)
}

// handleMyContext returns the caller's own tenant (Tenant Admins).
func (s *Server) handleMyContext(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if id.Role != model.RoleTenant {
		writeErr(w, http.StatusBadRequest, "platform admins have no single tenant context; use a tenant drill-in")
		return
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeTenantContext(w, r, t)
}

// handleTenantContext returns a specific tenant (Platform drill-in; Tenant only own).
func (s *Server) handleTenantContext(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	slug := chi.URLParam(r, "slug")

	t, err := s.store.TenantBySlug(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id.Role != model.RolePlatform && !strings.EqualFold(t.TenantID, id.TID) {
		writeErr(w, http.StatusForbidden, "not authorized for this tenant")
		return
	}
	s.writeTenantContext(w, r, t)
}

func (s *Server) writeTenantContext(w http.ResponseWriter, r *http.Request, t model.Tenant) {
	agents, err := s.store.Agents(r.Context(), t.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The tenant's enabled resources, so the console can draw its topology from a
	// single context call (works for the platform drill-in too, which has no
	// tenant-scoped session of its own).
	infra, err := s.store.InfrastructureForTenant(r.Context(), t.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	apps, err := s.store.ApplicationsForTenant(r.Context(), t.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stores, err := s.store.MemoryStoresForTenant(r.Context(), t.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, model.TenantContextResponse{
		Tenant:         t,
		Agents:         gateAgentHealth(t, agents),
		Infrastructure: enabledInfra(infra),
		Applications:   enabledApps(apps),
		Stores:         enabledStores(stores),
	})
}

// enabled* keep only the resources actually enabled in the tenant (what's
// provisioned in its subscription) — the topology shows the live footprint, not
// the whole entitlement catalog.
func enabledInfra(in []model.Infrastructure) []model.Infrastructure {
	out := []model.Infrastructure{}
	for _, i := range in {
		if i.Enabled {
			out = append(out, i)
		}
	}
	return out
}
func enabledApps(in []model.Application) []model.Application {
	out := []model.Application{}
	for _, a := range in {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out
}
func enabledStores(in []model.MemoryStore) []model.MemoryStore {
	out := []model.MemoryStore{}
	for _, s := range in {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// gateAgentHealth blanks out health the control plane can't vouch for. An agent's
// stored health is only as trustworthy as the reconciler that reported it: unless
// the tenant is live (fresh heartbeat), that state is stale or never existed, so
// agents read 'unknown' (unreported) rather than a confident healthy/blocked. This
// keeps desired (enabled) and actual (reconciler-confirmed) from being conflated.
func gateAgentHealth(t model.Tenant, agents []model.Agent) []model.Agent {
	if t.Lifecycle == "live" {
		return agents
	}
	for i := range agents {
		agents[i].Health = "unknown"
	}
	return agents
}

/* ── Unified create ──────────────────────────────────────────────────────── */

// handleApply is the unified transactional create: one request creates any
// combination of infrastructure, memory stores, agents, and applications, in
// dependency order, atomically. Scoped by the caller — platform admins author
// platform resources (owner ""); a tenant authors its own (owner = its slug,
// ids namespaced). The single-resource create endpoints are thin wrappers; a CLI
// can send a whole application graph in one call.
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	var body struct {
		Infrastructure []struct {
			Name         string             `json:"name"`
			Description  string             `json:"description"`
			BicepModule  string             `json:"bicepModule"`
			BicepParams  map[string]any     `json:"bicepParams"`
			Dependencies []model.Dependency `json:"dependencies"`
		} `json:"infrastructure"`
		MemoryStores []struct {
			Name        string                       `json:"name"`
			Description string                       `json:"description"`
			Definition  shared.MemoryStoreDefinition `json:"definition"`
		} `json:"memoryStores"`
		Agents []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Type        string                 `json:"type"`
			Model       string                 `json:"model"`
			Definition  shared.AgentDefinition `json:"definition"`
		} `json:"agents"`
		Applications []struct {
			Name           string             `json:"name"`
			Description    string             `json:"description"`
			Namespace      string             `json:"namespace"`
			RepoURL        string             `json:"repoURL"`
			Chart          string             `json:"chart"`
			TargetRevision string             `json:"targetRevision"`
			Values         string             `json:"values"`
			Wiring         []shared.WireLink  `json:"wiring"`
			Dependencies   []model.Dependency `json:"dependencies"`
		} `json:"applications"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	// Scope: platform authors platform resources; a tenant authors its own,
	// with ids namespaced to avoid collisions with platform slugs.
	owner, prefix := "", ""
	if id.Role == model.RoleTenant {
		t, ok := s.callerTenant(w, r)
		if !ok {
			return
		}
		owner, prefix = t.ID, t.ID+"-"
	}

	var batch store.ApplyBatch
	for _, in := range body.Infrastructure {
		name := strings.TrimSpace(in.Name)
		if slugify(name) == "" {
			writeErr(w, http.StatusBadRequest, "infrastructure name is required")
			return
		}
		if strings.TrimSpace(in.BicepModule) == "" {
			writeErr(w, http.StatusBadRequest, "a Bicep module reference is required")
			return
		}
		infra := model.Infrastructure{
			ID: prefix + slugify(name), Name: name, Description: strings.TrimSpace(in.Description),
			Owner: owner, BicepModule: strings.TrimSpace(in.BicepModule),
			BicepParams: in.BicepParams, Dependencies: in.Dependencies,
		}
		if !s.resolveInfra(w, r, &infra) {
			return
		}
		batch.Infrastructure = append(batch.Infrastructure, infra)
	}
	for _, ms := range body.MemoryStores {
		name := strings.TrimSpace(ms.Name)
		if slugify(name) == "" {
			writeErr(w, http.StatusBadRequest, "memory store name is required")
			return
		}
		batch.MemoryStores = append(batch.MemoryStores, model.MemoryStore{
			ID: prefix + slugify(name), Name: name, Description: strings.TrimSpace(ms.Description),
			Owner: owner, Definition: ms.Definition,
		})
	}
	for _, ag := range body.Agents {
		name := strings.TrimSpace(ag.Name)
		if slugify(name) == "" {
			writeErr(w, http.StatusBadRequest, "agent name is required")
			return
		}
		agentType := "prompt"
		if ag.Type == "hosted" {
			agentType = "hosted"
		}
		agentModel := strings.TrimSpace(ag.Model)
		if agentModel == "" {
			agentModel = "gpt-4o"
		}
		batch.Agents = append(batch.Agents, store.ApplyAgent{
			ID: prefix + slugify(name), Name: name, Description: strings.TrimSpace(ag.Description),
			Type: agentType, Model: agentModel, Owner: owner, Definition: ag.Definition,
		})
	}
	for _, ap := range body.Applications {
		name := strings.TrimSpace(ap.Name)
		if slugify(name) == "" {
			writeErr(w, http.StatusBadRequest, "application name is required")
			return
		}
		batch.Applications = append(batch.Applications, model.Application{
			ID: prefix + slugify(name), Name: name, Description: strings.TrimSpace(ap.Description),
			Owner: owner, Namespace: strings.TrimSpace(ap.Namespace), RepoURL: strings.TrimSpace(ap.RepoURL),
			Chart: strings.TrimSpace(ap.Chart), TargetRevision: strings.TrimSpace(ap.TargetRevision),
			Values: ap.Values, Wiring: ap.Wiring, Dependencies: ap.Dependencies,
		})
	}

	res, err := s.store.Apply(r.Context(), id.OID, batch)
	if err != nil {
		if errors.Is(err, store.ErrBadDependency) || errors.Is(err, store.ErrDependencyCycle) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if isDup(err) {
			writeErr(w, http.StatusConflict, "a resource with that name already exists")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// catalogWriteAllowed loads an agent and checks the caller may modify it: platform
// admins may version any agent; a tenant only its own.
func (s *Server) catalogWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, agentID string) bool {
	owner, err := s.store.CatalogAgentOwner(r.Context(), agentID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return false
	}
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if id.Role == model.RolePlatform {
		return true
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if owner != t.ID {
		writeErr(w, http.StatusForbidden, "not your agent")
		return false
	}
	return true
}

/* ── Tenants registry + entitlements (platform) ──────────────────────────── */

func (s *Server) handleTenantsRegistry(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	rows, err := s.store.TenantsRegistry(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": rows})
}

/* ── Memory stores (platform-authored + tenant-created) ──────────────────── */

// storeWriteAllowed loads a store and checks the caller may modify it: platform
// admins may modify any store; a tenant only its own.
func (s *Server) storeWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, storeID string) bool {
	ms, err := s.store.MemoryStoreByID(r.Context(), storeID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "memory store not found")
		return false
	}
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if id.Role == model.RolePlatform {
		return true
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if ms.Owner != t.ID {
		writeErr(w, http.StatusForbidden, "not your memory store")
		return false
	}
	return true
}

/* ── Tenant desired state ────────────────────────────────────────────────── */

// handleConnectAgentStore connects (or, with an empty storeId, disconnects) a
// tenant's enabled agent to a memory store it owns or is entitled to.
func (s *Server) handleConnectAgentStore(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		StoreID string `json:"storeId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	switch err := s.store.ConnectAgentStore(r.Context(), t.ID, chi.URLParam(r, "agentId"), strings.TrimSpace(body.StoreID)); {
	case errors.Is(err, store.ErrStoreNotAccessible):
		writeErr(w, http.StatusForbidden, "that memory store isn't available to your tenant")
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "agent not enabled")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
	}
}

/* ── Deployments — catalog entities (like memory stores) ─────────────────── */

// appWriteAllowed loads a deployment and checks the caller may modify it:
// platform admins any, a tenant only its own.
func (s *Server) appWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, appID string) bool {
	a, err := s.store.ApplicationByID(r.Context(), appID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "deployment not found")
		return false
	}
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if id.Role == model.RolePlatform {
		return true
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if a.Owner != t.ID {
		writeErr(w, http.StatusForbidden, "not your deployment")
		return false
	}
	return true
}

func (s *Server) handleInspectModule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BicepModule string `json:"bicepModule"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	params, outputs, err := bicep.Inspect(r.Context(), strings.TrimSpace(body.BicepModule))
	if err != nil {
		if errors.Is(err, bicep.ErrNoCompiler) {
			// No toolchain — the form degrades to the JSON editor client-side.
			writeJSON(w, http.StatusOK, map[string]any{"params": []any{}, "outputs": []any{}, "resolved": false})
			return
		}
		writeErr(w, http.StatusBadRequest, "bicep module: "+err.Error())
		return
	}
	if params == nil {
		params = []bicep.ParamSpec{}
	}
	if outputs == nil {
		outputs = []bicep.OutputSpec{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"params": params, "outputs": outputs, "resolved": true})
}

// handleInspectChart reads a Helm chart's default values + optional JSON Schema so
// the console can render a typed values builder. Missing toolchain degrades to the
// raw YAML editor client-side; a bad ref / unreachable chart is a 400.
func (s *Server) handleInspectChart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RepoURL string `json:"repoURL"`
		Chart   string `json:"chart"`
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	iface, err := chart.Inspect(r.Context(), body.RepoURL, body.Chart, body.Version)
	if err != nil {
		if errors.Is(err, chart.ErrNoHelm) {
			writeJSON(w, http.StatusOK, map[string]any{"resolved": false})
			return
		}
		writeErr(w, http.StatusBadRequest, "chart: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resolved":    true,
		"name":        iface.Name,
		"version":     iface.Version,
		"description": iface.Description,
		"defaults":    iface.Defaults,
		"schema":      iface.Schema,
	})
}

/* ── Infrastructure (Bicep/Azure catalog entities) ────────────────────────── */

// infraWriteAllowed loads an infrastructure entity and checks the caller may
// modify it: platform admins any, a tenant only its own. Returns the owner.
func (s *Server) infraWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, infraID string) (string, bool) {
	owner, err := s.store.InfrastructureOwner(r.Context(), infraID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "infrastructure not found")
		return "", false
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	if id.Role == model.RolePlatform {
		return owner, true
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	if owner != t.ID {
		writeErr(w, http.StatusForbidden, "not your infrastructure")
		return "", false
	}
	return owner, true
}

/* ── Reconciler (in-tenant, Entra-token auth; tenant = token tid) ─────────── */

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.ReconIdentityFrom(r.Context())
	if !ok || id.TID == "" {
		writeErr(w, http.StatusUnauthorized, "unauthenticated reconciler")
		return
	}
	ds, err := s.store.SyncDesired(r.Context(), id.TID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Pin this tenant's cluster ingress to accept only its own Entra tokens,
	// addressed to the Cortex app registration (issuer derived from the token's
	// tenant, so a token can't be reused across tenants).
	ds.IngressAuth = s.ingressAuthForTenant(id.TID)
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.ReconIdentityFrom(r.Context())
	if !ok || id.TID == "" {
		writeErr(w, http.StatusUnauthorized, "unauthenticated reconciler")
		return
	}
	var hb shared.Heartbeat
	if !decodeJSON(w, r, &hb) {
		return
	}
	// The tenant comes from the validated token, not the request body — the
	// reconciler can only ever report for its own tenant.
	hb.TenantID = id.TID
	if err := s.store.ApplyHeartbeat(r.Context(), hb); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

/* ── Authz + request helpers ─────────────────────────────────────────────── */

func (s *Server) requirePlatform(w http.ResponseWriter, id model.Identity) bool {
	if id.Role != model.RolePlatform {
		writeErr(w, http.StatusForbidden, "platform admins only")
		return false
	}
	return true
}

func (s *Server) callerTenant(w http.ResponseWriter, r *http.Request) (model.Tenant, bool) {
	id, _ := auth.IdentityFrom(r.Context())
	if id.Role != model.RoleTenant {
		writeErr(w, http.StatusBadRequest, "this is a tenant-scoped action")
		return model.Tenant{}, false
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return model.Tenant{}, false
	}
	return t, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func isDup(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "path", r.URL.Path, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only advertise CORS when a specific origin is configured — never an
		// empty or wildcard allow-origin. Requests are authorized by bearer
		// token (not cookies), so a single fixed origin is the whole allowlist.
		if s.corsOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func orgNameFromEmail(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 && i+1 < len(email) {
		return email[i+1:]
	}
	return ""
}
