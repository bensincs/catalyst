// Package foundry converges a tenant's Microsoft Foundry project toward the
// desired set of agents, calling the Foundry Agent Service data-plane API
// (Assistants-compatible) with the reconciler's own Entra identity.
//
// Per AGENT-MODEL.md §1, a prompt agent — model + instructions + tools +
// params — maps directly onto a Foundry "assistant": this package creates,
// updates, and deletes assistants so the project matches desired state. Cortex
// stamps identifying metadata on every assistant it creates so it only ever
// mutates or prunes agents it owns, never anything else in the project.
//
// Hosted (bring-your-own-container) agents are realized by a separate
// compute-deploy path, not the Agent Service, so they are reported honestly as
// unprovisioned here rather than given fabricated health.
package foundry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/inception42/cortex/reconciler/internal/config"
	"github.com/inception42/cortex/reconciler/internal/tokens"
	"github.com/inception42/cortex/shared"
)

// Health states reported back to the control plane (shared.AgentStatus.Health).
const (
	healthHealthy = "healthy"
	healthBlocked = "blocked"
)

// Metadata keys stamped on every Cortex-managed assistant, so the reconciler can
// recognize the agents it owns and detect a version change without a brittle
// field-by-field diff. Foundry persists this metadata, so convergence survives a
// reconciler restart (unlike the previous in-memory stub).
const (
	metaManaged = "cortex_managed"  // "true"
	metaAgentID = "cortex_agent_id" // control-plane agent id
	metaVersion = "cortex_version"  // desired version last converged
)

// Foundry is a client for one tenant's Foundry project (one reconciler drives
// one project). It is used sequentially by the reconcile loop.
type Foundry struct {
	http       *http.Client
	endpoint   string // https://<resource>.services.ai.azure.com/api/projects/<project>
	apiVersion string
	project    string // display name — logging only
	tokens     tokens.Source
}

// New builds a Foundry client. ts must yield tokens for the Foundry scope
// (https://ai.azure.com/.default by default).
func New(cfg config.Config, ts tokens.Source) *Foundry {
	return &Foundry{
		http:       &http.Client{Timeout: 30 * time.Second},
		endpoint:   cfg.FoundryEndpoint,
		apiVersion: cfg.FoundryAPIVersion,
		project:    cfg.FoundryProject,
		tokens:     ts,
	}
}

// Reconcile drives the project toward desired and returns the actual state of
// every desired agent. It creates missing prompt agents, updates drifted ones,
// prunes managed agents that are no longer desired, and reports honest state:
// telemetry it cannot yet measure stays zero, and anything it cannot realize
// (a failed call, or a hosted agent) is reported blocked rather than healthy.
func (f *Foundry) Reconcile(ctx context.Context, desired []shared.DesiredAgent) []shared.AgentStatus {
	out := make([]shared.AgentStatus, 0, len(desired))

	// The agents Cortex already manages in this project, keyed by control-plane
	// agent id. If we can't read the project we can't assert anything true about
	// actual state, so every prompt agent is reported blocked (never invented).
	managed, listErr := f.listManaged(ctx)
	if listErr != nil {
		slog.Warn("foundry: list agents failed; reporting prompt agents as blocked", "project", f.project, "err", listErr)
	}

	desiredPrompt := make(map[string]bool, len(desired))
	for _, d := range desired {
		if d.Type == shared.AgentHosted {
			// Hosted agents are container deployments (separate compute path),
			// not Foundry assistants — report unprovisioned, don't fabricate.
			out = append(out, blocked(d.AgentID, ""))
			continue
		}
		desiredPrompt[d.AgentID] = true
		if listErr != nil {
			out = append(out, blocked(d.AgentID, ""))
			continue
		}
		out = append(out, f.converge(ctx, d, managed[d.AgentID]))
	}

	// Prune managed prompt agents that are no longer desired (de-provisioned).
	// Skipped entirely when the list failed, so a transient read error never
	// deletes live agents.
	if listErr == nil {
		for agentID, a := range managed {
			if desiredPrompt[agentID] {
				continue
			}
			if err := f.deleteAssistant(ctx, a.ID); err != nil {
				slog.Warn("foundry: delete de-provisioned agent failed", "agentId", agentID, "assistant", a.ID, "err", err)
			} else {
				slog.Info("foundry: deleted de-provisioned agent", "agentId", agentID, "assistant", a.ID)
			}
		}
	}

	return out
}

// converge creates or updates the assistant backing one prompt agent and returns
// its actual status. existing is the currently-managed assistant for this agent,
// or nil if none exists yet.
func (f *Foundry) converge(ctx context.Context, d shared.DesiredAgent, existing *assistant) shared.AgentStatus {
	want := f.specFor(d)

	if existing == nil {
		if _, err := f.writeAssistant(ctx, "", want); err != nil {
			slog.Warn("foundry: create agent failed", "agentId", d.AgentID, "err", err)
			return blocked(d.AgentID, "")
		}
		slog.Info("foundry: created agent", "agentId", d.AgentID, "version", d.Version)
		return healthy(d.AgentID, d.Version)
	}

	if matches(*existing, want, d.Version) {
		return healthy(d.AgentID, d.Version)
	}

	if _, err := f.writeAssistant(ctx, existing.ID, want); err != nil {
		slog.Warn("foundry: update agent failed", "agentId", d.AgentID, "assistant", existing.ID, "err", err)
		// The previous version is still what's actually live — report that.
		return blocked(d.AgentID, existing.Metadata[metaVersion])
	}
	slog.Info("foundry: updated agent", "agentId", d.AgentID, "version", d.Version)
	return healthy(d.AgentID, d.Version)
}

// specFor derives the desired Foundry assistant spec from a prompt agent.
func (f *Foundry) specFor(d shared.DesiredAgent) assistantSpec {
	tools := make([]toolDef, 0, len(d.Definition.Tools))
	for _, t := range d.Definition.Tools {
		if t = strings.TrimSpace(t); t != "" {
			tools = append(tools, toolDef{Type: t})
		}
	}
	return assistantSpec{
		Model:        d.Model,
		Name:         d.Name,
		Instructions: d.Definition.Instructions,
		Tools:        tools,
		Temperature:  d.Definition.Temperature,
		Metadata: map[string]string{
			metaManaged: "true",
			metaAgentID: d.AgentID,
			metaVersion: d.Version,
		},
	}
}

// matches reports whether the live assistant already equals the desired spec at
// the desired version. Temperature is only compared when desired (a nil desired
// temperature means "don't care", so Foundry's default doesn't cause a perpetual
// update loop).
func matches(have assistant, want assistantSpec, wantVersion string) bool {
	if have.Metadata[metaVersion] != wantVersion {
		return false
	}
	if have.Model != want.Model || have.Name != want.Name || have.Instructions != want.Instructions {
		return false
	}
	if want.Temperature != nil && (have.Temperature == nil || *have.Temperature != *want.Temperature) {
		return false
	}
	return sameToolTypes(have.Tools, want.Tools)
}

func sameToolTypes(a, b []toolDef) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		as[i] = a[i].Type
	}
	for i := range b {
		bs[i] = b[i].Type
	}
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func healthy(agentID, version string) shared.AgentStatus {
	return shared.AgentStatus{AgentID: agentID, Version: version, Health: healthHealthy, Calls30d: 0}
}

// blocked reports an agent that couldn't be realized. version is whatever is
// actually live (empty if nothing is), never the unachieved desired version.
//
// Calls30d is 0 here and in healthy(): 30-day call volume is real usage
// telemetry (Azure Monitor / Foundry metrics) the reconciler does not yet read,
// so it stays zero rather than showing a fabricated number.
func blocked(agentID, version string) shared.AgentStatus {
	return shared.AgentStatus{AgentID: agentID, Version: version, Health: healthBlocked, Calls30d: 0}
}

// --- Foundry Agent Service (Assistants-compatible) wire types ---------------

type toolDef struct {
	Type string `json:"type"`
}

// assistant is the subset of the assistant object Cortex reads back.
type assistant struct {
	ID           string            `json:"id"`
	Model        string            `json:"model"`
	Name         string            `json:"name"`
	Instructions string            `json:"instructions"`
	Tools        []toolDef         `json:"tools"`
	Temperature  *float64          `json:"temperature"`
	Metadata     map[string]string `json:"metadata"`
}

// assistantSpec is the create/update request body.
type assistantSpec struct {
	Model        string            `json:"model"`
	Name         string            `json:"name,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Tools        []toolDef         `json:"tools"`
	Temperature  *float64          `json:"temperature,omitempty"`
	Metadata     map[string]string `json:"metadata"`
}

type assistantList struct {
	Data    []assistant `json:"data"`
	HasMore bool        `json:"has_more"`
	LastID  string      `json:"last_id"`
}

// --- data-plane calls -------------------------------------------------------

// listManaged pages through the project's assistants and returns those Cortex
// owns, keyed by control-plane agent id.
func (f *Foundry) listManaged(ctx context.Context) (map[string]*assistant, error) {
	managed := map[string]*assistant{}
	after := ""
	for {
		u := fmt.Sprintf("%s/assistants?api-version=%s&limit=100", f.endpoint, url.QueryEscape(f.apiVersion))
		if after != "" {
			u += "&after=" + url.QueryEscape(after)
		}
		var page assistantList
		if err := f.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			a := page.Data[i]
			if a.Metadata[metaManaged] != "true" {
				continue // not ours — leave it untouched
			}
			if id := a.Metadata[metaAgentID]; id != "" {
				managed[id] = &a
			}
		}
		if !page.HasMore || page.LastID == "" {
			return managed, nil
		}
		after = page.LastID
	}
}

// writeAssistant creates (id == "") or updates (id != "") an assistant. The
// Agent Service, like the Assistants API, modifies via POST to the item path.
func (f *Foundry) writeAssistant(ctx context.Context, id string, s assistantSpec) (*assistant, error) {
	u := fmt.Sprintf("%s/assistants?api-version=%s", f.endpoint, url.QueryEscape(f.apiVersion))
	if id != "" {
		u = fmt.Sprintf("%s/assistants/%s?api-version=%s", f.endpoint, url.PathEscape(id), url.QueryEscape(f.apiVersion))
	}
	var out assistant
	if err := f.do(ctx, http.MethodPost, u, s, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (f *Foundry) deleteAssistant(ctx context.Context, id string) error {
	u := fmt.Sprintf("%s/assistants/%s?api-version=%s", f.endpoint, url.PathEscape(id), url.QueryEscape(f.apiVersion))
	return f.do(ctx, http.MethodDelete, u, nil, nil)
}

// do issues one authenticated JSON request, decoding the response into out when
// non-nil. A non-2xx status is returned as an error including the response body.
func (f *Foundry) do(ctx context.Context, method, u string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	tok, err := f.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("acquire foundry token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s %s/assistants: %d %s", method, f.endpoint, resp.StatusCode, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
