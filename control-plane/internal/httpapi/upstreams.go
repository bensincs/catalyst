package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/inception42/cortex/control-plane/internal/auth"
)

// Registry upstreams — the private registries mirrored into the platform
// registry, managed by a platform admin from the console rather than by a
// redeploy. The registry itself is the source of truth, so these handlers read
// and write Azure directly and keep no copy that could drift from it.

func (s *Server) handleListUpstreams(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if s.upstreams == nil {
		// Cross-tenant provisioning off ⇒ no registry to manage. An empty list
		// with the registry unnamed is what the console renders as "unavailable".
		writeJSON(w, http.StatusOK, map[string]any{"registry": "", "upstreams": []any{}})
		return
	}
	list, err := s.upstreams.ListUpstreams(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registry": s.platformRegistry, "upstreams": list})
}

func (s *Server) handlePutUpstream(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if s.upstreams == nil {
		writeErr(w, http.StatusBadRequest, "no platform registry configured")
		return
	}
	var body struct {
		Source   string `json:"source"`
		Target   string `json:"target"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if err := s.upstreams.PutUpstream(r.Context(), name, body.Source, body.Target, body.Username, body.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUpstream(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.IdentityFrom(r.Context())
	if !s.requirePlatform(w, id) {
		return
	}
	if s.upstreams == nil {
		writeErr(w, http.StatusBadRequest, "no platform registry configured")
		return
	}
	if err := s.upstreams.DeleteUpstream(r.Context(), chi.URLParam(r, "name")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
