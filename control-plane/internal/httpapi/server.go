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
	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/control-plane/internal/store"
	"github.com/inception42/cortex/shared"
)

type Server struct {
	store      *store.Store
	auth       *auth.Authenticator
	recon      *auth.ReconAuthenticator
	corsOrigin string
}

func NewServer(st *store.Store, a *auth.Authenticator, recon *auth.ReconAuthenticator, corsOrigin string) *Server {
	return &Server{store: st, auth: a, recon: recon, corsOrigin: corsOrigin}
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
		r.Get("/me", s.handleMe)
		r.Get("/fleet", s.handleFleet)
		r.Get("/tenant/context", s.handleMyContext)
		r.Get("/tenants/{slug}/context", s.handleTenantContext)

		// Catalog
		r.Get("/catalog", s.handleCatalog)
		r.Post("/catalog", s.handleCreateCatalogAgent)
		r.Post("/catalog/{id}/versions", s.handlePublishVersion)

		// Tenant registry + entitlements (platform)
		r.Get("/tenants", s.handleTenantsRegistry)
		r.Patch("/tenants/{slug}/entitlements", s.handleSetEntitlements)
		r.Patch("/tenants/{slug}/store-entitlements", s.handleSetStoreEntitlements)

		// Memory stores (platform-authored + tenant-created)
		r.Get("/memory-stores", s.handleMemoryStores)
		r.Post("/memory-stores", s.handleCreateMemoryStore)
		r.Patch("/memory-stores/{id}", s.handleUpdateMemoryStore)
		r.Delete("/memory-stores/{id}", s.handleDeleteMemoryStore)

		// Tenant desired state
		r.Post("/tenant/agents", s.handleEnableAgent)
		r.Delete("/tenant/agents/{agentId}", s.handleDisableAgent)
		r.Post("/tenant/agents/{agentId}/store", s.handleConnectAgentStore)
		r.Post("/tenant/stores/{storeId}", s.handleEnableStore)
		r.Delete("/tenant/stores/{storeId}", s.handleDisableStore)
	})

	// Reconciler-facing endpoints. The in-tenant reconciler authenticates with
	// its own Entra token (managed identity in Azure; dev secret locally); the
	// tenant it acts on is the token's tid — never a client-supplied parameter.
	r.Route("/recon", func(r chi.Router) {
		r.Use(s.recon.Middleware)
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
	writeJSON(w, http.StatusOK, model.TenantContextResponse{Tenant: t, Agents: gateAgentHealth(t, agents)})
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

/* ── Catalog ─────────────────────────────────────────────────────────────── */

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if id.Role == model.RolePlatform {
		list, err := s.store.CatalogList(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": list})
		return
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.store.CatalogForTenant(r.Context(), t.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": list})
}

func (s *Server) handleCreateCatalogAgent(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	var body struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Type        string                 `json:"type"`
		Model       string                 `json:"model"`
		Definition  shared.AgentDefinition `json:"definition"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	slug := slugify(body.Name)
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	agentType := "prompt"
	if body.Type == "hosted" {
		agentType = "hosted"
	}
	agentModel := strings.TrimSpace(body.Model)
	if agentModel == "" {
		agentModel = "gpt-4o"
	}
	owner := "" // platform-authored by default
	if id.Role == model.RoleTenant {
		t, ok := s.callerTenant(w, r)
		if !ok {
			return
		}
		owner = t.ID
		slug = t.ID + "-" + slug // namespace tenant agents to avoid platform-slug collisions
	}
	if err := s.store.CreateCatalogAgent(r.Context(), slug, strings.TrimSpace(body.Name),
		strings.TrimSpace(body.Description), agentType, agentModel, owner, id.OID, body.Definition); err != nil {
		if isDup(err) {
			writeErr(w, http.StatusConflict, "an agent with that name already exists")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": slug})
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

func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	agentID := chi.URLParam(r, "id")
	if !s.catalogWriteAllowed(w, r, id, agentID) {
		return
	}
	var body struct {
		Version        string                 `json:"version"`
		Channel        string                 `json:"channel"`
		Notes          string                 `json:"notes"`
		RolloutPercent int                    `json:"rolloutPercent"`
		Definition     shared.AgentDefinition `json:"definition"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	version := strings.TrimSpace(body.Version)
	if version == "" {
		writeErr(w, http.StatusBadRequest, "version is required")
		return
	}
	channel := "stable"
	if body.Channel == "beta" {
		channel = "beta"
	}
	rollout := body.RolloutPercent
	if rollout <= 0 || rollout > 100 {
		rollout = 100
	}
	if err := s.store.PublishVersion(r.Context(), agentID, version, channel, strings.TrimSpace(body.Notes), rollout, body.Definition); err != nil {
		if isDup(err) {
			writeErr(w, http.StatusConflict, "that version already exists")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "published"})
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

func (s *Server) handleSetEntitlements(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		EntitledAgents []string `json:"entitledAgents"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.EntitledAgents == nil {
		body.EntitledAgents = []string{}
	}
	if err := s.store.SetEntitlements(r.Context(), chi.URLParam(r, "slug"), body.EntitledAgents); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

/* ── Memory stores (platform-authored + tenant-created) ──────────────────── */

func (s *Server) handleMemoryStores(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if id.Role == model.RolePlatform {
		list, err := s.store.MemoryStoreList(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stores": list})
		return
	}
	t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.store.MemoryStoresForTenant(r.Context(), t.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stores": list})
}

func (s *Server) handleCreateMemoryStore(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	var body struct {
		Name        string                       `json:"name"`
		Description string                       `json:"description"`
		Definition  shared.MemoryStoreDefinition `json:"definition"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	slug := slugify(body.Name)
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	def := body.Definition
	// The models are required by Foundry; default to the standard project
	// deployments so a store is always provisionable.
	if strings.TrimSpace(def.ChatModel) == "" {
		def.ChatModel = "gpt-4o"
	}
	if strings.TrimSpace(def.EmbeddingModel) == "" {
		def.EmbeddingModel = "text-embedding-3-small"
	}
	owner := "" // platform-authored by default
	if id.Role == model.RoleTenant {
		t, ok := s.callerTenant(w, r)
		if !ok {
			return
		}
		owner = t.ID
		slug = t.ID + "-" + slug // namespace tenant stores to avoid platform-slug collisions
	}
	if err := s.store.CreateMemoryStore(r.Context(), slug, strings.TrimSpace(body.Name),
		strings.TrimSpace(body.Description), owner, def, id.OID); err != nil {
		if isDup(err) {
			writeErr(w, http.StatusConflict, "a memory store with that name already exists")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": slug})
}

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

func (s *Server) handleUpdateMemoryStore(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	storeID := chi.URLParam(r, "id")
	if !s.storeWriteAllowed(w, r, id, storeID) {
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
	if err := s.store.UpdateMemoryStore(r.Context(), storeID, strings.TrimSpace(body.Name),
		strings.TrimSpace(body.Description)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "memory store not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleDeleteMemoryStore(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	storeID := chi.URLParam(r, "id")
	if !s.storeWriteAllowed(w, r, id, storeID) {
		return
	}
	if err := s.store.DeleteMemoryStore(r.Context(), storeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "memory store not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleSetStoreEntitlements(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		EntitledStores []string `json:"entitledStores"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.EntitledStores == nil {
		body.EntitledStores = []string{}
	}
	if err := s.store.SetStoreEntitlements(r.Context(), chi.URLParam(r, "slug"), body.EntitledStores); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

/* ── Tenant desired state ────────────────────────────────────────────────── */

func (s *Server) handleEnableAgent(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		CatalogAgentID string   `json:"catalogAgentId"`
		PublishTo      []string `json:"publishTo"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.CatalogAgentID == "" {
		writeErr(w, http.StatusBadRequest, "catalogAgentId is required")
		return
	}
	switch err := s.store.EnableAgent(r.Context(), t.ID, body.CatalogAgentID, body.PublishTo); {
	case errors.Is(err, store.ErrNotEntitled):
		writeErr(w, http.StatusForbidden, "not entitled to that agent")
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "agent not found")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
	}
}

func (s *Server) handleDisableAgent(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	if err := s.store.DisableAgent(r.Context(), t.ID, chi.URLParam(r, "agentId")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

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

// handleEnableStore activates a memory store (owned or entitled) in the caller's
// tenant, mirroring enabling an agent — the reconciler then provisions it.
func (s *Server) handleEnableStore(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	switch err := s.store.EnableStore(r.Context(), t.ID, chi.URLParam(r, "storeId")); {
	case errors.Is(err, store.ErrStoreNotAccessible):
		writeErr(w, http.StatusForbidden, "that memory store isn't available to your tenant")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
	}
}

func (s *Server) handleDisableStore(w http.ResponseWriter, r *http.Request) {
	t, ok := s.callerTenant(w, r)
	if !ok {
		return
	}
	switch err := s.store.DisableStore(r.Context(), t.ID, chi.URLParam(r, "storeId")); {
	case errors.Is(err, store.ErrStoreInUse):
		writeErr(w, http.StatusConflict, "that store is in use by an enabled agent — disconnect it first")
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "store not enabled")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
	}
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
		w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Vary", "Origin")
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
