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
// Memory stores are first-class Foundry resources too (POST /memory_stores,
// api-version "2025-11-15-preview", also keyed by name and stamped with Cortex
// metadata). The reconciler provisions each referenced store, then binds an agent
// to it by adding a memory_search_preview tool that names the store. A store's
// definition (its models + memory kinds) is immutable — the resource has no
// update surface — so on drift the reconciler warns rather than destroy the
// store's accumulated memories.
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

// Metadata keys stamped on every Cortex-managed agent version and memory store,
// so the reconciler recognizes the resources it owns and detects a version
// change without a brittle field-by-field diff. Foundry persists this metadata,
// so convergence survives a reconciler restart.
const (
	metaManaged = "cortex_managed"  // "true"
	metaAgentID = "cortex_agent_id" // control-plane agent id
	metaVersion = "cortex_version"  // desired version last converged
	metaStoreID = "cortex_store_id" // control-plane memory-store id
)

// promptKind is the definition discriminator for a declarative prompt agent.
const promptKind = "prompt"

// Memory-store constants. Memory stores live on a preview api-version distinct
// from the agents surface. An agent binds to a store via a memory_search_preview
// tool that names the store and scopes it — by default to the signed-in user, so
// memories are isolated per end user ({{$userId}} is a Foundry template var).
const (
	memStoreAPIVersion = "2025-11-15-preview"
	memStoreKind       = "default"
	memoryToolType     = "memory_search_preview"
	defaultMemoryScope = "{{$userId}}"
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
// every desired agent. It creates missing prompt agents, publishes a new version
// for drifted ones, prunes managed agents that are no longer desired, and reports
// honest state: telemetry it cannot yet measure stays zero, and anything it
// cannot realize (a failed call, or a hosted agent) is reported blocked.
func (f *Foundry) Reconcile(ctx context.Context, desired []shared.DesiredAgent, stores []shared.DesiredMemoryStore) []shared.AgentStatus {
	out := make([]shared.AgentStatus, 0, len(desired))

	// Provision the memory stores referenced by desired agents as first-class
	// Foundry resources. The returned map (control-plane store id → Foundry store
	// name) covers only stores that are actually provisioned, so an agent is bound
	// only to a store that exists — never to a dangling name.
	storeNames := f.reconcileStores(ctx, stores)

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
		out = append(out, f.converge(ctx, d, managed[d.AgentID], storeNames))
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
func (f *Foundry) converge(ctx context.Context, d shared.DesiredAgent, existing *agent, storeNames map[string]string) shared.AgentStatus {
	def, skipped := f.definitionFor(d, storeNames)
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
// storeNames maps memory-store id → provisioned Foundry store name; when the
// agent connects a store, a memory_search_preview tool binding it (by name,
// scoped per user) is added to the definition's tools.
func (f *Foundry) definitionFor(d shared.DesiredAgent, storeNames map[string]string) (definition, []string) {
	tools := make([]toolDef, 0, len(d.Definition.Tools)+1)
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
	// Bind a connected memory store — but only if it was actually provisioned
	// this cycle. If it wasn't (e.g. its embedding model isn't deployed), the
	// agent is left unbound rather than pointed at a store that doesn't exist;
	// reconcileStores has already logged why.
	if id := d.Definition.MemoryStore; id != "" {
		if name, ok := storeNames[id]; ok {
			tools = append(tools, toolDef{Type: memoryToolType, MemoryStoreName: name, Scope: defaultMemoryScope})
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
// was sent — it injects no defaults — so nil stays nil and never loops). Tools
// are compared structurally including the memory binding (store name + scope),
// so connecting, switching, or disconnecting a memory store republishes.
func sameDefinition(have, want definition) bool {
	if have.Model != want.Model || have.Instructions != want.Instructions {
		return false
	}
	if !sameFloat(have.Temperature, want.Temperature) || !sameFloat(have.TopP, want.TopP) {
		return false
	}
	return sameTools(have.Tools, want.Tools)
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// toolKey is a tool's identity for drift comparison: its type plus, for a memory
// tool, the store it names and its scope. Service-injected fields the reconciler
// doesn't set (e.g. update_delay) aren't part of toolDef and so don't cause a
// spurious republish.
func toolKey(t toolDef) string {
	return t.Type + "|" + t.MemoryStoreName + "|" + t.Scope
}

func sameTools(a, b []toolDef) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		as[i] = toolKey(a[i])
	}
	for i := range b {
		bs[i] = toolKey(b[i])
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

// agentName maps a control-plane id to a valid Foundry resource name (which, for
// agents and memory stores alike, is also the resource's id). Foundry names must
// start and end with an alphanumeric and use only hyphens in between (no '_',
// '.', ':' — max 63 chars), so any run of other characters collapses to a single
// hyphen. The true control-plane id is always carried in metadata, so lookups
// never rely on this.
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
	// Memory binding (type == memory_search_preview): the store to search, by
	// name, and the scope that isolates its memories (e.g. per signed-in user).
	MemoryStoreName string `json:"memory_store_name,omitempty"`
	Scope           string `json:"scope,omitempty"`
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

// --- Memory stores ----------------------------------------------------------

// memStoreOptions mirrors Foundry's MemoryStoreDefaultOptions: which memory
// kinds are extracted, and how long they live.
type memStoreOptions struct {
	UserProfileEnabled      bool   `json:"user_profile_enabled"`
	UserProfileDetails      string `json:"user_profile_details,omitempty"`
	ChatSummaryEnabled      bool   `json:"chat_summary_enabled"`
	ProceduralMemoryEnabled bool   `json:"procedural_memory_enabled"`
	DefaultTTLSeconds       int    `json:"default_ttl_seconds"`
}

// memStoreDefinition mirrors Foundry's MemoryStoreDefaultDefinition (kind
// "default"): the models that process memory plus the extraction options.
type memStoreDefinition struct {
	Kind           string          `json:"kind"`
	ChatModel      string          `json:"chat_model"`
	EmbeddingModel string          `json:"embedding_model"`
	Options        memStoreOptions `json:"options"`
}

type memStore struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Metadata   map[string]string  `json:"metadata"`
	Definition memStoreDefinition `json:"definition"`
}

type memStoreList struct {
	Data    []memStore `json:"data"`
	HasMore bool       `json:"has_more"`
	LastID  string     `json:"last_id"`
}

type memStoreCreateBody struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Metadata    map[string]string  `json:"metadata"`
	Definition  memStoreDefinition `json:"definition"`
}

// storeDefinitionFor maps the control-plane store definition onto the Foundry
// memory_store definition (kind "default", snake_case wire form).
func storeDefinitionFor(d shared.MemoryStoreDefinition) memStoreDefinition {
	return memStoreDefinition{
		Kind:           memStoreKind,
		ChatModel:      d.ChatModel,
		EmbeddingModel: d.EmbeddingModel,
		Options: memStoreOptions{
			UserProfileEnabled:      d.UserProfileEnabled,
			UserProfileDetails:      d.UserProfileDetails,
			ChatSummaryEnabled:      d.ChatSummaryEnabled,
			ProceduralMemoryEnabled: d.ProceduralMemoryEnabled,
			DefaultTTLSeconds:       d.TTLSeconds,
		},
	}
}

func storeMetadata(storeID string) map[string]string {
	return map[string]string{metaManaged: "true", metaStoreID: storeID}
}

// sameStoreDefinition reports whether a live store already matches the desired
// definition. memStoreOptions is all value types, so it compares by value.
func sameStoreDefinition(have, want memStoreDefinition) bool {
	return have.ChatModel == want.ChatModel &&
		have.EmbeddingModel == want.EmbeddingModel &&
		have.Options == want.Options
}

// reconcileStores provisions every referenced memory store as a first-class
// Foundry resource and returns the store id → Foundry name map for those that
// exist, so agents can be bound by name. It creates missing stores; a store it
// can't create (e.g. its embedding model isn't deployed) is logged and left out
// of the map, so agents referencing it are simply left unbound this cycle.
//
// Unlike agents, managed stores are never pruned: a store holds a user's
// accumulated memories, and its definition is immutable (the resource has no
// update surface), so the reconciler will not destroy and recreate one. Drift is
// reported, not "converged" by deletion.
func (f *Foundry) reconcileStores(ctx context.Context, desired []shared.DesiredMemoryStore) map[string]string {
	ready := make(map[string]string, len(desired))
	if len(desired) == 0 {
		return ready
	}
	managed, err := f.listManagedStores(ctx)
	if err != nil {
		// Can't assert the stores exist — skip binding rather than point agents
		// at names we haven't confirmed.
		slog.Warn("foundry: list memory stores failed; agents referencing a store are left unbound this cycle", "project", f.project, "err", err)
		return ready
	}
	for _, ds := range desired {
		if existing, ok := managed[ds.ID]; ok {
			if !sameStoreDefinition(existing.Definition, storeDefinitionFor(ds.Definition)) {
				slog.Warn("foundry: memory store definition drift; the store is immutable (no update surface), keeping the existing store and its memories",
					"storeId", ds.ID, "store", existing.Name)
			}
			ready[ds.ID] = existing.Name
			continue
		}
		name := agentName(ds.ID)
		if err := f.createStore(ctx, name, ds.Name, storeMetadata(ds.ID), storeDefinitionFor(ds.Definition)); err != nil {
			slog.Warn("foundry: create memory store failed; agents referencing it are left unbound (is its embedding model deployed?)",
				"storeId", ds.ID, "store", name, "err", err)
			continue
		}
		slog.Info("foundry: created memory store", "storeId", ds.ID, "store", name)
		ready[ds.ID] = name
	}
	return ready
}

// listManagedStores pages through the project's memory stores and returns those
// Cortex owns, keyed by control-plane store id (read from metadata).
func (f *Foundry) listManagedStores(ctx context.Context) (map[string]*memStore, error) {
	managed := map[string]*memStore{}
	after := ""
	for {
		u := fmt.Sprintf("%s/memory_stores?api-version=%s&limit=100", f.endpoint, url.QueryEscape(memStoreAPIVersion))
		if after != "" {
			u += "&after=" + url.QueryEscape(after)
		}
		var page memStoreList
		if err := f.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			ms := page.Data[i]
			if ms.Metadata[metaManaged] != "true" {
				continue // not ours — leave it untouched
			}
			id := ms.Metadata[metaStoreID]
			if id == "" {
				id = ms.Name
			}
			managed[id] = &ms
		}
		if !page.HasMore || page.LastID == "" {
			return managed, nil
		}
		after = page.LastID
	}
}

func (f *Foundry) createStore(ctx context.Context, name, description string, meta map[string]string, def memStoreDefinition) error {
	u := fmt.Sprintf("%s/memory_stores?api-version=%s", f.endpoint, url.QueryEscape(memStoreAPIVersion))
	body := memStoreCreateBody{Name: name, Description: description, Metadata: meta, Definition: def}
	return f.do(ctx, http.MethodPost, u, body, nil)
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
		return fmt.Errorf("foundry %s %s: %d %s", method, u, resp.StatusCode, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
