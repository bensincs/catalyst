// Package foundry converges a tenant's Microsoft Foundry project toward the
// desired set of agents, calling the Foundry Agents API (the new first-class
// /agents surface, api-version "v1") with the reconciler's own Entra identity.
//
// Per AGENT-MODEL.md §1, a prompt agent — model + instructions + tools +
// params — maps onto a Foundry agent whose latest version carries a
// kind:"prompt" definition. Agents are keyed by name (their id == name) and are
// versioned: an update publishes a new version that becomes versions.latest.
// Cortex stamps identifying metadata on every version it writes, so it only ever
// mutates or prunes agents it owns, never anything else in the project.
//
// Hosted (bring-your-own-container) agents are realized by a separate
// compute-deploy path, not the Agents API, so they are reported honestly as
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

// Metadata keys stamped on every Cortex-managed agent version, so the reconciler
// recognizes the agents it owns and detects a version change without a brittle
// field-by-field diff. Foundry persists this metadata per version, so
// convergence survives a reconciler restart.
const (
	metaManaged = "cortex_managed"  // "true"
	metaAgentID = "cortex_agent_id" // control-plane agent id
	metaVersion = "cortex_version"  // desired version last converged
)

// promptKind is the definition discriminator for a declarative prompt agent.
const promptKind = "prompt"

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
// every desired agent. It creates missing prompt agents, publishes a new version
// for drifted ones, prunes managed agents that are no longer desired, and reports
// honest state: telemetry it cannot yet measure stays zero, and anything it
// cannot realize (a failed call, or a hosted agent) is reported blocked.
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
			// not Foundry agents — report unprovisioned, don't fabricate.
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
			if err := f.deleteAgent(ctx, a.Name); err != nil {
				slog.Warn("foundry: delete de-provisioned agent failed", "agentId", agentID, "agent", a.Name, "err", err)
			} else {
				slog.Info("foundry: deleted de-provisioned agent", "agentId", agentID, "agent", a.Name)
			}
		}
	}

	return out
}

// converge creates the agent backing one prompt agent, or publishes a new
// version when it has drifted, and returns its actual status. existing is the
// currently-managed agent for this control-plane id, or nil if none exists yet.
func (f *Foundry) converge(ctx context.Context, d shared.DesiredAgent, existing *agent) shared.AgentStatus {
	def, skipped := f.definitionFor(d)
	meta := metadataFor(d)

	if existing == nil {
		if err := f.createAgent(ctx, agentName(d.AgentID), d.Name, meta, def); err != nil {
			slog.Warn("foundry: create agent failed", "agentId", d.AgentID, "err", err)
			return blocked(d.AgentID, "")
		}
		slog.Info("foundry: created agent", "agentId", d.AgentID, "version", d.Version)
		warnSkippedTools(d.AgentID, skipped)
		return healthy(d.AgentID, d.Version)
	}

	latest := existing.Versions.Latest
	if latest.Metadata[metaVersion] == d.Version && sameDefinition(latest.Definition, def) {
		return healthy(d.AgentID, d.Version) // already converged — no new version
	}

	if err := f.addVersion(ctx, existing.Name, d.Name, meta, def); err != nil {
		slog.Warn("foundry: publish agent version failed", "agentId", d.AgentID, "agent", existing.Name, "err", err)
		// The previous version is still what's actually live — report that.
		return blocked(d.AgentID, latest.Metadata[metaVersion])
	}
	slog.Info("foundry: published agent version", "agentId", d.AgentID, "agent", existing.Name, "version", d.Version)
	warnSkippedTools(d.AgentID, skipped)
	return healthy(d.AgentID, d.Version)
}

// bareTools are the Foundry agent tool types realizable from just their type
// name. The other tools the console offers — file_search (needs
// vector_store_ids) and function (needs a name/JSON schema) — plus the
// connection-based tools (bing_grounding, azure_ai_search, openapi, mcp) require
// configuration the control-plane contract (a plain tool name) doesn't carry
// yet, so they're skipped rather than sent malformed. This tracks the deferred
// knowledge-binding work in AGENT-MODEL.md; when tool config is modeled, map it
// here.
var bareTools = map[string]bool{
	"code_interpreter": true,
	"web":              true,
}

// definitionFor derives the desired kind:"prompt" definition from a prompt agent,
// returning the names of any tools skipped because they need unmodeled config.
func (f *Foundry) definitionFor(d shared.DesiredAgent) (definition, []string) {
	tools := make([]toolDef, 0, len(d.Definition.Tools))
	var skipped []string
	for _, t := range d.Definition.Tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if bareTools[t] {
			tools = append(tools, toolDef{Type: t})
		} else {
			skipped = append(skipped, t)
		}
	}
	def := definition{
		Kind:         promptKind,
		Model:        d.Model,
		Instructions: d.Definition.Instructions,
		Tools:        tools,
		Temperature:  d.Definition.Temperature,
		TopP:         d.Definition.TopP,
	}
	return def, skipped
}

func warnSkippedTools(agentID string, skipped []string) {
	if len(skipped) > 0 {
		slog.Warn("foundry: skipped tools that need config not yet in the contract",
			"agentId", agentID, "tools", strings.Join(skipped, ","))
	}
}

func metadataFor(d shared.DesiredAgent) map[string]string {
	return map[string]string{
		metaManaged: "true",
		metaAgentID: d.AgentID,
		metaVersion: d.Version,
	}
}

// sameDefinition reports whether the live definition already equals the desired
// one. Temperature/TopP are compared exactly (the Agents API stores only what
// was sent — it injects no defaults — so nil stays nil and never loops).
func sameDefinition(have, want definition) bool {
	if have.Model != want.Model || have.Instructions != want.Instructions {
		return false
	}
	if !sameFloat(have.Temperature, want.Temperature) || !sameFloat(have.TopP, want.TopP) {
		return false
	}
	return sameToolTypes(have.Tools, want.Tools)
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
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

// agentName maps a control-plane agent id to a valid Foundry agent name (which
// is also the agent's id). Foundry names must start and end with an alphanumeric
// and use only hyphens in between (no '_', '.', ':' — max 63 chars), so any run
// of other characters collapses to a single hyphen. The true control-plane id is
// always carried in metadata, so lookups never rely on this.
func agentName(agentID string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range agentID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if b.Len() > 0 && !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "agent"
	}
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}

// --- Foundry Agents API wire types ------------------------------------------

type toolDef struct {
	Type string `json:"type"`
}

// definition is a kind:"prompt" agent definition.
type definition struct {
	Kind         string    `json:"kind"`
	Model        string    `json:"model"`
	Instructions string    `json:"instructions,omitempty"`
	Tools        []toolDef `json:"tools"`
	Temperature  *float64  `json:"temperature,omitempty"`
	TopP         *float64  `json:"top_p,omitempty"`
}

// agentVersion is one version of an agent (agents.versions.latest).
type agentVersion struct {
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
	Definition  definition        `json:"definition"`
}

// agent is the subset of the agent object Cortex reads back.
type agent struct {
	ID       string `json:"id"` // == Name
	Name     string `json:"name"`
	State    string `json:"state"`
	Versions struct {
		Latest agentVersion `json:"latest"`
	} `json:"versions"`
}

type agentList struct {
	Data    []agent `json:"data"`
	HasMore bool    `json:"has_more"`
	LastID  string  `json:"last_id"`
}

// createBody is the POST /agents request. versionBody is POST /agents/{name}/versions.
type createBody struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata"`
	Definition  definition        `json:"definition"`
}

type versionBody struct {
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata"`
	Definition  definition        `json:"definition"`
}

// --- data-plane calls -------------------------------------------------------

// listManaged pages through the project's agents and returns those Cortex owns,
// keyed by control-plane agent id (read from the latest version's metadata).
func (f *Foundry) listManaged(ctx context.Context) (map[string]*agent, error) {
	managed := map[string]*agent{}
	after := ""
	for {
		u := fmt.Sprintf("%s/agents?api-version=%s&limit=100", f.endpoint, url.QueryEscape(f.apiVersion))
		if after != "" {
			u += "&after=" + url.QueryEscape(after)
		}
		var page agentList
		if err := f.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			a := page.Data[i]
			md := a.Versions.Latest.Metadata
			if md[metaManaged] != "true" {
				continue // not ours — leave it untouched
			}
			id := md[metaAgentID]
			if id == "" {
				id = a.Name
			}
			managed[id] = &a
		}
		if !page.HasMore || page.LastID == "" {
			return managed, nil
		}
		after = page.LastID
	}
}

func (f *Foundry) createAgent(ctx context.Context, name, description string, meta map[string]string, def definition) error {
	u := fmt.Sprintf("%s/agents?api-version=%s", f.endpoint, url.QueryEscape(f.apiVersion))
	body := createBody{Name: name, Description: description, Metadata: meta, Definition: def}
	return f.do(ctx, http.MethodPost, u, body, nil)
}

func (f *Foundry) addVersion(ctx context.Context, name, description string, meta map[string]string, def definition) error {
	u := fmt.Sprintf("%s/agents/%s/versions?api-version=%s", f.endpoint, url.PathEscape(name), url.QueryEscape(f.apiVersion))
	body := versionBody{Description: description, Metadata: meta, Definition: def}
	return f.do(ctx, http.MethodPost, u, body, nil)
}

func (f *Foundry) deleteAgent(ctx context.Context, name string) error {
	u := fmt.Sprintf("%s/agents/%s?api-version=%s", f.endpoint, url.PathEscape(name), url.QueryEscape(f.apiVersion))
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
		return fmt.Errorf("%s %s/agents: %d %s", method, f.endpoint, resp.StatusCode, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
