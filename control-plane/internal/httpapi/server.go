package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

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
	platformTID     string // the platform's own directory (ingress issuer for platform-hosted tenants)
	platformSub     string // the platform's own subscription (where platform-hosted tenants are created)

	// tenantTeardown deletes a tenant's Azure resource groups. Injected by main
	// from the infra provisioner (which owns the ARM credential); nil when
	// cross-tenant provisioning is off, in which case deleting a tenant removes
	// only its control-plane records.
	tenantTeardown func(ctx context.Context, sub, hostingMode, tenantRG string) error
}

// SetTenantTeardown wires the Azure teardown used when a tenant is deleted. The
// provisioner is constructed after the server, so this can't be a constructor arg.
func (s *Server) SetTenantTeardown(fn func(ctx context.Context, sub, hostingMode, tenantRG string) error) {
	s.tenantTeardown = fn
}

func NewServer(st *store.Store, a *auth.Authenticator, recon *auth.ReconAuthenticator, corsOrigin, entraClientID, entraIssuerHost, platformTID, platformSub string) *Server {
	return &Server{
		store:           st,
		auth:            a,
		recon:           recon,
		corsOrigin:      corsOrigin,
		entraClientID:   entraClientID,
		entraIssuerHost: entraIssuerHost,
		platformTID:     strings.ToLower(platformTID),
		platformSub:     strings.TrimSpace(platformSub),
	}
}

// reconTenantKey stashes the tenant a reconciler request resolved to (by identity
// or directory id) so the handlers don't re-resolve it.
type reconTenantKey struct{}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(s.cors)

	// Health must include the database. Every route except this one needs it, so
	// a bare 200 here reports "healthy" while the API can serve nothing — which
	// is exactly what happened when the Postgres server was stopped underneath
	// it: healthz stayed green while every real request hung until timeout.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.store.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "degraded",
				"error":  "database unreachable",
			})
			return
		}
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
			r.Post("/tenants", s.handleCreateTenant)
			r.Patch("/tenants/{slug}/all-entitlements", s.handleSetAllEntitlements)
			r.Patch("/tenants/{slug}/enabled", s.handleSetTenantEnabled)
			r.Patch("/tenants/{slug}/name", s.handleRenameTenant)
			r.Post("/tenants/{slug}/reprovision", s.handleReprovisionFootprint)
			r.Delete("/tenants/{slug}", s.handleDeleteTenant)
			// Deleted tenants: listed so an admin can see why a delegated
			// subscription isn't reappearing, and lift the deletion.
			r.Get("/tenant-tombstones", s.handleListTombstones)
			r.Delete("/tenant-tombstones/{slug}", s.handleRestoreTenant)
			// Ingress: the domain a tenant publishes apps on, and the OIDC
			// application that guards them.
			r.Patch("/tenants/{slug}/ingress", s.handleSetAppsDomain)
			r.Patch("/tenants/{slug}/oidc", s.handleSetOIDCConfig)
			// Footprint editor: configure the cluster shape, then stamp it.
			r.Patch("/tenants/{slug}/footprint", s.handleSetFootprintConfig)
			r.Post("/tenants/{slug}/footprint/stamp", s.handleStampFootprint)
			// Membership (platform-hosted tenants): assign/list/remove users.
			r.Get("/tenants/{slug}/members", s.handleListMembers)
			r.Post("/tenants/{slug}/members", s.handleAddMember)
			r.Delete("/tenants/{slug}/members/{principal}", s.handleRemoveMember)
			// Previously-signed-in users, for the members type-ahead.
			r.Get("/users/search", s.handleSearchUsers)

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

	// Bind any memberships created for this user's email before they'd signed in,
	// so oid-based authorization works from here on.
	_ = s.store.BindMemberships(r.Context(), id.OID, id.Email)

	if id.Role == model.RoleTenant {
		tenants, primary, err := s.accessibleTenants(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		resp.Tenants = tenants
		resp.Tenant = primary
		var primarySlug *string
		if primary != nil {
			primarySlug = &primary.ID
		}
		if err := s.store.UpsertUser(r.Context(), id, primarySlug); err != nil {
			s.fail(w, r, err)
			return
		}
	} else if err := s.store.UpsertUser(r.Context(), id, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// accessibleTenants returns every tenant a non-platform caller can act on: their
// delegated directory tenant (JIT-created, if they sign in from their own
// directory) plus any platform-hosted tenants they're assigned to. The first is
// the primary/default the console lands on.
func (s *Server) accessibleTenants(ctx context.Context, id model.Identity) ([]model.Tenant, *model.Tenant, error) {
	seen := map[string]bool{}
	var out []model.Tenant
	// A caller signing in from their own (non-platform) directory owns that
	// directory's delegated tenant — JIT-create it so it surfaces for approval.
	if !strings.EqualFold(id.TID, s.platformTID) {
		t, err := s.store.EnsureTenantForTID(ctx, id.TID, orgNameFromEmail(id.Email))
		// A deleted tenant isn't an error here — the caller simply owns no
		// directory tenant. Any explicit memberships below still apply.
		if err != nil && !errors.Is(err, store.ErrTenantDeleted) {
			return nil, nil, err
		}
		if err == nil {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	members, err := s.store.MembershipTenants(ctx, id.OID, id.Email)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range members {
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	var primary *model.Tenant
	for i := range out {
		if out[i].Enabled {
			primary = &out[i]
			break
		}
	}
	if primary == nil && len(out) > 0 {
		primary = &out[0]
	}
	return out, primary, nil
}

// tenantGate blocks callers with no enabled tenant from every /api route except
// /me. Platform admins always pass. A caller from their own directory needs that
// (delegated) tenant enabled; a platform-directory member needs at least one
// enabled tenant assignment. First contact records a delegated tenant (disabled)
// so it surfaces for platform approval, then rejects it (pending-approval screen).
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
		// A caller from their own (non-platform) directory: their delegated tenant
		// must be enabled. JIT-create it so it surfaces for approval.
		if !strings.EqualFold(id.TID, s.platformTID) {
			t, err := s.store.EnsureTenantForTID(r.Context(), id.TID, orgNameFromEmail(id.Email))
			if errors.Is(err, store.ErrTenantDeleted) {
				// Deleted on purpose. Fall through to memberships rather than
				// silently re-creating the tenant this caller just lost.
				if member, _ := s.store.HasEnabledMembership(r.Context(), id.OID, id.Email); member {
					next.ServeHTTP(w, r)
					return
				}
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant deleted", "code": "tenant_deleted"})
				return
			}
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if t.Enabled {
				next.ServeHTTP(w, r)
				return
			}
			// A disabled directory tenant still passes if the user has some other
			// enabled assignment; otherwise it's the pending-approval case.
			if member, _ := s.store.HasEnabledMembership(r.Context(), id.OID, id.Email); !member {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant not enabled", "code": "tenant_disabled"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// A platform-directory member (not an admin): needs ≥1 enabled assignment.
		member, err := s.store.HasEnabledMembership(r.Context(), id.OID, id.Email)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !member {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no tenant access", "code": "no_membership"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// reconGate resolves + authorizes the reconciler's tenant and stashes it for the
// handlers. Platform-hosted tenants are identified by their pre-created
// reconciler managed-identity oid (their token tid is the shared platform
// directory); delegated tenants by the token tid. An unknown delegated tenant is
// recorded (disabled) so it surfaces for approval, then rejected.
func (s *Server) reconGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.ReconIdentityFrom(r.Context())
		if !ok || id.TID == "" {
			writeErr(w, http.StatusUnauthorized, "unauthenticated reconciler")
			return
		}
		// Platform-hosted: match the reconciler's managed-identity oid.
		t, err := s.store.TenantByReconcilerOID(r.Context(), id.OID)
		if errors.Is(err, store.ErrNotFound) {
			// No identity match. A platform-directory token with no recorded
			// reconciler identity is not a known tenant (the platform runs no
			// tenant reconciler of its own) — reject rather than mint one.
			if strings.EqualFold(id.TID, s.platformTID) {
				writeErr(w, http.StatusForbidden, "unrecognized reconciler")
				return
			}
			// Delegated: the tenant is the token's own directory.
			t, err = s.store.EnsureTenantForTID(r.Context(), id.TID, "")
		}
		if errors.Is(err, store.ErrTenantDeleted) {
			// The tenant was deleted while its reconciler was still running.
			// Refuse the sync so it stops, rather than resurrecting the tenant.
			writeErr(w, http.StatusForbidden, "tenant deleted")
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !t.Enabled {
			writeErr(w, http.StatusForbidden, "tenant not enabled")
			return
		}
		ctx := context.WithValue(r.Context(), reconTenantKey{}, t)
		next.ServeHTTP(w, r.WithContext(ctx))
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

// handleReprovisionFootprint re-submits a tenant's footprint (platform only) so
// template changes — config fixes, new features — reach an already-provisioned
// tenant. Same code path as stamping (StampFootprint): reprovision is just a
// re-stamp of an already-ready footprint.
func (s *Server) handleReprovisionFootprint(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if err := s.store.StampFootprint(r.Context(), chi.URLParam(r, "slug")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found or has no subscription")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reprovisioning"})
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
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
	t, ok := s.callerTenant(w, r)
	if !ok {
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
	if !s.authorizeTenant(r.Context(), id, t) {
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
		Infrastructure []infraInput `json:"infrastructure"`
		MemoryStores   []storeInput `json:"memoryStores"`
		Agents         []agentInput `json:"agents"`
		Applications   []appInput   `json:"applications"`
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

	// Same validate()/build() the update path uses, so create and edit can never
	// drift. Any bad item aborts the whole batch before it touches the store.
	var batch store.ApplyBatch
	for _, in := range body.Infrastructure {
		if msg := in.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		infra := in.build(prefix+slugify(in.Name), owner)
		if !s.resolveInfra(w, r, &infra) {
			return
		}
		batch.Infrastructure = append(batch.Infrastructure, infra)
	}
	for _, in := range body.MemoryStores {
		if msg := in.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		batch.MemoryStores = append(batch.MemoryStores, in.build(prefix+slugify(in.Name), owner))
	}
	for _, in := range body.Agents {
		if msg := in.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		batch.Agents = append(batch.Agents, in.applyAgent(prefix+slugify(in.Name), owner))
	}
	for _, in := range body.Applications {
		if msg := in.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		batch.Applications = append(batch.Applications, in.build(prefix+slugify(in.Name), owner))
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

// ownerWrite is the platform-or-owner half of every write-permission check:
// platform admins may modify anything; a tenant only what it owns. Authorization
// against the owning tenant is by membership or directory id, so it works for
// both delegated and platform-hosted tenants. The caller loads the owner (per
// kind) and passes the noun for the 403 message.
func (s *Server) ownerWrite(w http.ResponseWriter, r *http.Request, id model.Identity, owner, noun string) (string, bool) {
	if id.Role == model.RolePlatform {
		return owner, true
	}
	if owner == "" {
		writeErr(w, http.StatusForbidden, "not your "+noun)
		return "", false
	}
	t, err := s.store.TenantBySlug(r.Context(), owner)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusForbidden, "not your "+noun)
			return "", false
		}
		s.fail(w, r, err)
		return "", false
	}
	if !s.authorizeTenant(r.Context(), id, t) {
		writeErr(w, http.StatusForbidden, "not your "+noun)
		return "", false
	}
	return owner, true
}

// catalogWriteAllowed loads an agent and checks the caller may modify it: platform
// admins may edit any agent; a tenant only its own. Returns the owner.
func (s *Server) catalogWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, agentID string) (string, bool) {
	owner, err := s.store.CatalogAgentOwner(r.Context(), agentID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return "", false
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	return s.ownerWrite(w, r, id, owner, "agent")
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

// handleCreateTenant creates a platform-hosted tenant (platform only): a tenant
// in the platform's OWN subscription, a dedicated resource group per tenant, with
// no Entra directory of its own. Users are assigned to it via memberships. The
// provisioner deploys its footprint next sweep.
func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if s.platformSub == "" {
		writeErr(w, http.StatusPreconditionFailed, "platform-hosted tenants require PLATFORM_SUBSCRIPTION_ID to be configured")
		return
	}
	var body struct {
		Name   string `json:"name"`
		Region string `json:"region"`
		Plan   string `json:"plan"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	t, err := s.store.CreatePlatformTenant(r.Context(), body.Name, body.Region, body.Plan, s.platformSub)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// handleListMembers lists a tenant's assigned users (platform only).
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	members, err := s.store.MembershipsForTenant(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// handleAddMember assigns a user to a tenant (platform only), by email or by
// Entra object id.
func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	slug := chi.URLParam(r, "slug")
	if _, err := s.store.TenantBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	var body struct {
		Member string `json:"member"`
		Email  string `json:"email"` // accepted as an alias for member (an email)
		Role   string `json:"role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	member := strings.TrimSpace(body.Member)
	if member == "" {
		member = strings.TrimSpace(body.Email)
	}
	if !strings.Contains(member, "@") && !isGUID(member) {
		writeErr(w, http.StatusBadRequest, "provide an email address or an Entra object id")
		return
	}
	if err := s.store.AddMembership(r.Context(), slug, member, body.Role); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// handleRemoveMember revokes a user's assignment to a tenant (platform only), by
// its principal (email or oid).
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if err := s.store.RemoveMembership(r.Context(), chi.URLParam(r, "slug"), chi.URLParam(r, "principal")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleRenameTenant sets a tenant's display name (platform only).
func (s *Server) handleRenameTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.store.RenameTenant(r.Context(), chi.URLParam(r, "slug"), body.Name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "renamed"})
}

// handleDeleteTenant removes a tenant, tears down its Azure resource groups and
// tombstones the slug so discovery can't bring it back (platform only).
//
// Order matters: Azure first. The tenant_infrastructure rows are the only record
// of what was provisioned and they cascade away with the tenant, so deleting the
// row first would strand live, billing resources with nothing pointing at them.
//
// ?purgeAzure=false keeps the Azure resources and removes only the Cortex
// records — for when the subscription is being handed back intact.
func (s *Server) handleDeleteTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	slug := chi.URLParam(r, "slug")
	t, err := s.store.TenantBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}

	purge := r.URL.Query().Get("purgeAzure") != "false"
	azureTornDown := false
	if purge && s.tenantTeardown != nil {
		if err := s.tenantTeardown(r.Context(), t.SubscriptionID, t.HostingMode, t.ResourceGroup); err != nil {
			// Refuse to delete the records while their Azure counterparts may
			// still exist — otherwise the resources are orphaned and unbillable
			// to anyone. The admin can retry, or pass purgeAzure=false.
			writeErr(w, http.StatusBadGateway,
				"couldn't delete the tenant's Azure resource groups: "+err.Error()+
					" — retry, or use ?purgeAzure=false to remove only the Cortex records")
			return
		}
		azureTornDown = true
	}

	who := id.Email
	if who == "" {
		who = id.OID
	}
	if err := s.store.DeleteTenant(r.Context(), slug, who); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "deleted",
		"azureTornDown": azureTornDown,
	})
}

// handleListTombstones lists deleted tenants (platform only).
func (s *Server) handleListTombstones(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	ts, err := s.store.Tombstones(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tombstones": ts})
}

// handleRestoreTenant lifts a deletion so the tenant can be discovered or signed
// into again (platform only). It does not restore any data — a delegated tenant
// simply reappears, disabled, on the next sweep.
func (s *Server) handleRestoreTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if err := s.store.RemoveTombstone(r.Context(), chi.URLParam(r, "slug")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "no such deleted tenant")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// handleSetAppsDomain sets the domain a tenant publishes apps on (platform only).
//
// Changing it resets delegation and discards the wildcard certificate, which was
// issued for the previous domain. The control plane then creates the zone and
// surfaces the nameservers the customer must delegate to.
func (s *Server) handleSetAppsDomain(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		AppsDomain string `json:"appsDomain"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	domain := strings.ToLower(strings.Trim(strings.TrimSpace(body.AppsDomain), "."))
	if domain != "" && !isDomainName(domain) {
		writeErr(w, http.StatusBadRequest, "that doesn't look like a domain name — expected something like apps.contoso.com")
		return
	}
	if err := s.store.SetAppsDomain(r.Context(), chi.URLParam(r, "slug"), domain); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleSetOIDCConfig records the customer's OIDC application (platform only).
// An empty clientSecret leaves the stored one untouched, so the client id can be
// corrected without re-entering a secret that is never shown back.
func (s *Server) handleSetOIDCConfig(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		Issuer       string `json:"oidcIssuer"`
		ClientID     string `json:"oidcClientId"`
		ClientSecret string `json:"oidcClientSecret"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	issuer := strings.TrimSpace(body.Issuer)
	if issuer != "" && !strings.HasPrefix(issuer, "https://") {
		writeErr(w, http.StatusBadRequest, "the OIDC issuer must be an https:// URL, e.g. https://login.microsoftonline.com/<tenant-id>/v2.0")
		return
	}
	if err := s.store.SetOIDCConfig(r.Context(), chi.URLParam(r, "slug"), issuer, body.ClientID, body.ClientSecret); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// isDomainName is a light sanity check: at least two labels, each RFC1123.
func isDomainName(s string) bool {
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// handleSearchUsers returns previously-signed-in users matching a query, for the
// members type-ahead (platform only).
func (s *Server) handleSearchUsers(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	users, err := s.store.SearchUsers(r.Context(), r.URL.Query().Get("q"), 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// handleSetFootprintConfig records a tenant's footprint config (AKS node sizing)
// before it's stamped (platform only).
func (s *Server) handleSetFootprintConfig(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	var body struct {
		Config map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.store.SetFootprintConfig(r.Context(), chi.URLParam(r, "slug"), body.Config); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleStampFootprint queues a tenant's footprint for provisioning — the
// deferred "stamp" a platform admin presses after configuring it (platform only).
func (s *Server) handleStampFootprint(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	slug := chi.URLParam(r, "slug")
	if err := s.store.StampFootprint(r.Context(), slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "tenant not found or has no subscription")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stamping"})
}

/* ── Memory stores (platform-authored + tenant-created) ──────────────────── */

// storeWriteAllowed loads a store and checks the caller may modify it: platform
// admins may modify any store; a tenant only its own. Returns the owner.
func (s *Server) storeWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, storeID string) (string, bool) {
	ms, err := s.store.MemoryStoreByID(r.Context(), storeID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "memory store not found")
		return "", false
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	return s.ownerWrite(w, r, id, ms.Owner, "memory store")
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
// platform admins any, a tenant only its own. Returns the owner.
func (s *Server) appWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, appID string) (string, bool) {
	a, err := s.store.ApplicationByID(r.Context(), appID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "deployment not found")
		return "", false
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	return s.ownerWrite(w, r, id, a.Owner, "deployment")
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
		// Identify the release so rendered Service names are the real ones. When
		// editing, AppID is the deployment's id; when creating it does not exist
		// yet, so Name is slugged with the same rule create uses. The server owns
		// that rule, so the client must not try to predict it.
		AppID  string `json:"appId"`
		Name   string `json:"name"`
		Values string `json:"values"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	release := strings.TrimSpace(body.AppID)
	if release == "" && strings.TrimSpace(body.Name) != "" {
		prefix := ""
		if id, _ := auth.IdentityFrom(r.Context()); id.Role == model.RoleTenant {
			t, ok := s.callerTenant(w, r)
			if !ok {
				return
			}
			prefix = t.ID + "-"
		}
		release = prefix + slugify(body.Name)
	}
	iface, err := chart.Inspect(r.Context(), body.RepoURL, body.Chart, body.Version, release, body.Values)
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
		"services":    iface.Services,
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
	return s.ownerWrite(w, r, id, owner, "infrastructure")
}

/* ── Reconciler (in-tenant, Entra-token auth; tenant = token tid) ─────────── */

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	t, ok := r.Context().Value(reconTenantKey{}).(model.Tenant)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthenticated reconciler")
		return
	}
	ds, err := s.store.SyncDesired(r.Context(), t)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Pin this tenant's cluster ingress to accept only Entra tokens addressed to
	// the Cortex app registration, issued by the tenant's directory (delegated) or
	// the platform directory (platform-hosted).
	ds.IngressAuth = s.ingressAuthForTenant(t)
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	t, ok := r.Context().Value(reconTenantKey{}).(model.Tenant)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthenticated reconciler")
		return
	}
	var hb shared.Heartbeat
	if !decodeJSON(w, r, &hb) {
		return
	}
	// The tenant comes from the validated identity, not the request body — the
	// reconciler can only ever report for its own tenant.
	if err := s.store.ApplyHeartbeat(r.Context(), t, hb); err != nil {
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
	// The console's active selection is honored when the caller is authorized for
	// it (which requires it to be enabled); a stale or now-disabled selection
	// falls through to the caller's primary so the console is never stranded.
	if slug := strings.TrimSpace(r.Header.Get("X-Cortex-Tenant")); slug != "" {
		if t, err := s.store.TenantBySlug(r.Context(), slug); err == nil && s.authorizeTenant(r.Context(), id, t) {
			return t, true
		}
	}
	// Otherwise the caller's primary: the first ENABLED tenant they can access
	// (their delegated directory tenant, or an assigned one).
	_, primary, err := s.accessibleTenants(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return model.Tenant{}, false
	}
	if primary != nil && primary.Enabled {
		return *primary, true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant not enabled", "code": "tenant_disabled"})
	return model.Tenant{}, false
}

// authorizeTenant reports whether a caller may act on a specific tenant: platform
// admins may act on any (including disabled ones, to re-enable/inspect); everyone
// else needs the tenant to be ENABLED and to either be from its own directory
// (delegated) or be explicitly assigned to it (membership). The enabled check is
// per-tenant here (not just the blanket tenantGate), so disabling one of a user's
// tenants cuts off exactly that tenant, not all-or-nothing.
func (s *Server) authorizeTenant(ctx context.Context, id model.Identity, t model.Tenant) bool {
	if id.Role == model.RolePlatform {
		return true
	}
	if !t.Enabled {
		return false
	}
	if t.HostingMode == model.HostingDelegated && t.TenantID != "" && strings.EqualFold(t.TenantID, id.TID) {
		return true
	}
	member, _ := s.store.IsMember(ctx, t.ID, id.OID, id.Email)
	return member
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
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Cortex-Tenant")
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

// isGUID reports whether s is an Entra object id (a GUID) — the alternative to an
// email when assigning a tenant member.
func isGUID(s string) bool {
	return guidPattern.MatchString(strings.TrimSpace(s))
}

var guidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
