package foundry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/inception42/cortex/reconciler/internal/config"
	"github.com/inception42/cortex/shared"
)

type staticToken struct{}

func (staticToken) Token(context.Context) (string, error) { return "test-token", nil }

func mkAgent(name string, v agentVersion) agent {
	a := agent{ID: name, Name: name, State: "enabled"}
	a.Versions.Latest = v
	return a
}

// fakeFoundry is an in-memory stand-in for the subset of the Foundry Agents +
// memory-store API the reconciler drives: agent list/create/publish/delete, and
// memory-store list/create.
type fakeFoundry struct {
	mu                         sync.Mutex
	items                      map[string]agent
	stores                     map[string]memStore // keyed by name
	creates, versions, deletes int
	storeCreates               int
	failList                   bool
	failStoreCreate            bool // simulate e.g. an undeployed embedding model
}

func newFake() *fakeFoundry {
	return &fakeFoundry{items: map[string]agent{}, stores: map[string]memStore{}}
}

func (s *fakeFoundry) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if s.failList {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			data := make([]agent, 0, len(s.items))
			for _, a := range s.items {
				data = append(data, a)
			}
			_ = json.NewEncoder(w).Encode(agentList{Data: data})
		case http.MethodPost: // create
			var b createBody
			_ = json.NewDecoder(r.Body).Decode(&b)
			if _, ok := s.items[b.Name]; ok {
				http.Error(w, `{"error":{"code":"conflict"}}`, http.StatusConflict)
				return
			}
			s.items[b.Name] = mkAgent(b.Name, agentVersion{Version: "1", Description: b.Description, Metadata: b.Metadata, Definition: b.Definition})
			s.creates++
			_ = json.NewEncoder(w).Encode(s.items[b.Name])
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/agents/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		rest := strings.TrimPrefix(r.URL.Path, "/agents/")
		switch {
		case strings.HasSuffix(rest, "/versions") && r.Method == http.MethodPost:
			name := strings.TrimSuffix(rest, "/versions")
			a, ok := s.items[name]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			var b versionBody
			_ = json.NewDecoder(r.Body).Decode(&b)
			n, _ := strconv.Atoi(a.Versions.Latest.Version)
			a.Versions.Latest = agentVersion{Version: strconv.Itoa(n + 1), Description: b.Description, Metadata: b.Metadata, Definition: b.Definition}
			s.items[name] = a
			s.versions++
			_ = json.NewEncoder(w).Encode(a)
		case r.Method == http.MethodDelete:
			delete(s.items, rest)
			s.deletes++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": rest, "deleted": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/memory_stores", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			data := make([]memStore, 0, len(s.stores))
			for _, ms := range s.stores {
				data = append(data, ms)
			}
			_ = json.NewEncoder(w).Encode(memStoreList{Data: data})
		case http.MethodPost: // create
			var b memStoreCreateBody
			_ = json.NewDecoder(r.Body).Decode(&b)
			if s.failStoreCreate {
				http.Error(w, `{"error":{"code":"not_found","message":"Embedding model deployment not found"}}`, http.StatusBadRequest)
				return
			}
			if _, ok := s.stores[b.Name]; ok {
				http.Error(w, `{"error":{"code":"conflict"}}`, http.StatusConflict)
				return
			}
			s.stores[b.Name] = memStore{ID: "memstore_" + b.Name, Name: b.Name, Metadata: b.Metadata, Definition: b.Definition}
			s.storeCreates++
			_ = json.NewEncoder(w).Encode(s.stores[b.Name])
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func newClient(endpoint string) *Foundry {
	return New(config.Config{
		FoundryEndpoint:   endpoint,
		FoundryAPIVersion: "v1",
		FoundryProject:    "test",
	}, staticToken{})
}

// reconcileAgents runs a full reconcile and returns just the agent statuses (the
// subset most tests assert on); store statuses are covered separately.
func reconcileAgents(f *Foundry, ctx context.Context, d []shared.DesiredAgent, s []shared.DesiredMemoryStore) []shared.AgentStatus {
	agents, _ := f.Reconcile(ctx, d, s)
	return agents
}

func promptAgent() shared.DesiredAgent {
	temp := 0.5
	return shared.DesiredAgent{
		AgentID: "a1", Name: "Support", Type: shared.AgentPrompt, Version: "v1", Model: "gpt-4o",
		Definition: shared.AgentDefinition{
			Instructions: "help", Tools: []string{"code_interpreter"}, Temperature: &temp,
		},
	}
}

// The core convergence contract: create when missing, no-op when unchanged,
// publish a new version on a change, prune when de-provisioned.
func TestReconcile_CreateNoopVersionPrune(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)
	ctx := context.Background()
	a := promptAgent()

	st := reconcileAgents(f, ctx, []shared.DesiredAgent{a}, nil)
	if len(st) != 1 || st[0].Health != healthLive || st[0].Version != "v1" {
		t.Fatalf("create: got %+v", st)
	}
	if fake.creates != 1 || fake.versions != 0 || fake.deletes != 0 {
		t.Fatalf("create counts: c=%d v=%d d=%d", fake.creates, fake.versions, fake.deletes)
	}

	if st := reconcileAgents(f, ctx, []shared.DesiredAgent{a}, nil); st[0].Health != healthLive {
		t.Fatalf("noop health: %+v", st)
	}
	if fake.creates != 1 || fake.versions != 0 {
		t.Fatalf("noop must not write: c=%d v=%d", fake.creates, fake.versions)
	}

	a2 := a
	a2.Version = "v2"
	a2.Definition.Instructions = "help more"
	st = reconcileAgents(f, ctx, []shared.DesiredAgent{a2}, nil)
	if st[0].Health != healthLive || st[0].Version != "v2" {
		t.Fatalf("version status: %+v", st)
	}
	if fake.versions != 1 {
		t.Fatalf("expected 1 new version, got %d", fake.versions)
	}

	if st := reconcileAgents(f, ctx, nil, nil); len(st) != 0 {
		t.Fatalf("expected no statuses after prune, got %+v", st)
	}
	if fake.deletes != 1 || len(fake.items) != 0 {
		t.Fatalf("prune: deletes=%d remaining=%d", fake.deletes, len(fake.items))
	}
}

// Hosted agents are a separate compute-deploy path: report blocked, never touch
// Foundry.
func TestReconcile_HostedReportedBlocked(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)

	h := shared.DesiredAgent{
		AgentID: "h1", Name: "Worker", Type: shared.AgentHosted, Version: "v1",
		Definition: shared.AgentDefinition{Image: "ghcr.io/x:1"},
	}
	st := reconcileAgents(f, context.Background(), []shared.DesiredAgent{h}, nil)
	if len(st) != 1 || st[0].Health != healthBlocked || st[0].Version != "" {
		t.Fatalf("hosted: %+v", st)
	}
	if fake.creates != 0 {
		t.Fatalf("hosted must not create in foundry, got %d", fake.creates)
	}
}

// A failed read must not fabricate health and must not mutate the project.
func TestReconcile_ListFailureBlocksWithoutMutating(t *testing.T) {
	fake := newFake()
	fake.failList = true
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)

	a := promptAgent()
	st := reconcileAgents(f, context.Background(), []shared.DesiredAgent{a}, nil)
	if len(st) != 1 || st[0].Health != healthBlocked {
		t.Fatalf("expected blocked on list failure, got %+v", st)
	}
	if fake.creates != 0 || fake.versions != 0 || fake.deletes != 0 {
		t.Fatalf("must not mutate when list fails: c=%d v=%d d=%d", fake.creates, fake.versions, fake.deletes)
	}
}

// The reconciler owns only what it stamped: agents authored elsewhere in the
// project must survive a prune.
func TestReconcile_LeavesUnmanagedAgentsAlone(t *testing.T) {
	fake := newFake()
	fake.items["ext"] = mkAgent("ext", agentVersion{
		Version:    "1",
		Metadata:   map[string]string{"owner": "someone-else"},
		Definition: definition{Kind: promptKind, Model: "gpt-4o"},
	})
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)

	_ = reconcileAgents(f, context.Background(), nil, nil)
	if fake.deletes != 0 {
		t.Fatalf("must not delete unmanaged agents, got %d", fake.deletes)
	}
	if _, ok := fake.items["ext"]; !ok {
		t.Fatalf("unmanaged agent was removed")
	}
}

func TestAgentNameSanitizes(t *testing.T) {
	cases := map[string]string{
		"sdfsd":            "sdfsd",
		"a1":               "a1",
		"my agent:v2":      "my-agent-v2",
		"weird/id.ok_yes-": "weird-id-ok-yes",
	}
	for in, want := range cases {
		if got := agentName(in); got != want {
			t.Errorf("agentName(%q)=%q want %q", in, got, want)
		}
	}
}

// Tools that need configuration the contract doesn't carry (file_search,
// function, connection-based) are skipped; bare-type tools are kept in order.
func TestDefinitionFor_SkipsUnconfigurableTools(t *testing.T) {
	f := newClient("http://x")
	d := shared.DesiredAgent{
		AgentID: "a1", Model: "gpt-4o",
		Definition: shared.AgentDefinition{Tools: []string{"code_interpreter", "file_search", "web", "function"}},
	}
	def, skipped := f.definitionFor(d, nil)

	var kept []string
	for _, td := range def.Tools {
		kept = append(kept, td.Type)
	}
	if len(kept) != 2 || kept[0] != "code_interpreter" || kept[1] != "web" {
		t.Fatalf("kept tools = %v, want [code_interpreter web]", kept)
	}
	if len(skipped) != 2 || skipped[0] != "file_search" || skipped[1] != "function" {
		t.Fatalf("skipped = %v, want [file_search function]", skipped)
	}
}

// A connected memory store is provisioned as a first-class Foundry resource and
// bound to the agent via a memory_search_preview tool that names it. Switching
// which store an agent connects republishes a new version even when the agent
// version is unchanged (the effective definition changed).
func TestReconcile_MemoryStoreBinding(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)
	ctx := context.Background()

	a := shared.DesiredAgent{
		AgentID: "a1", Name: "Support", Type: shared.AgentPrompt, Version: "v1", Model: "gpt-4o",
		Definition: shared.AgentDefinition{Instructions: "help", MemoryStore: "mem-1"},
	}
	def := shared.MemoryStoreDefinition{ChatModel: "gpt-4o", EmbeddingModel: "text-embedding-3-small", UserProfileEnabled: true, ChatSummaryEnabled: true}
	stores := []shared.DesiredMemoryStore{{ID: "mem-1", Name: "Mem", Definition: def}}

	// definitionFor binds a provisioned store as a memory_search_preview tool,
	// by name, scoped per signed-in user.
	agentDef, _ := f.definitionFor(a, map[string]string{"mem-1": "mem-1"})
	var memTool *toolDef
	for i := range agentDef.Tools {
		if agentDef.Tools[i].Type == memoryToolType {
			memTool = &agentDef.Tools[i]
		}
	}
	if memTool == nil || memTool.MemoryStoreName != "mem-1" || memTool.Scope != defaultMemoryScope {
		t.Fatalf("memory tool not bound: %+v", agentDef.Tools)
	}

	// Reconcile provisions the store as a Foundry resource, then creates the agent.
	if st := reconcileAgents(f, ctx, []shared.DesiredAgent{a}, stores); st[0].Health != healthLive {
		t.Fatalf("create: %+v", st)
	}
	if fake.storeCreates != 1 {
		t.Fatalf("expected 1 memory store create, got %d", fake.storeCreates)
	}
	if fake.creates != 1 {
		t.Fatalf("expected 1 agent create, got %d", fake.creates)
	}
	// The provisioned store carries Cortex metadata + the typed definition.
	got, ok := fake.stores["mem-1"]
	if !ok || got.Metadata[metaStoreID] != "mem-1" || got.Metadata[metaManaged] != "true" ||
		got.Definition.Kind != memStoreKind || got.Definition.EmbeddingModel != "text-embedding-3-small" ||
		!got.Definition.Options.UserProfileEnabled {
		t.Fatalf("store not provisioned as modeled: %+v", fake.stores)
	}

	// Unchanged: neither the store nor the agent is rewritten.
	reconcileAgents(f, ctx, []shared.DesiredAgent{a}, stores)
	if fake.versions != 0 || fake.storeCreates != 1 {
		t.Fatalf("unchanged must not rewrite: versions=%d storeCreates=%d", fake.versions, fake.storeCreates)
	}

	// Connecting the agent to a different store republishes it (its
	// memory_search_preview tool now names a different store).
	a2 := a
	a2.Definition.MemoryStore = "mem-2"
	stores2 := []shared.DesiredMemoryStore{{ID: "mem-2", Name: "Mem2", Definition: def}}
	reconcileAgents(f, ctx, []shared.DesiredAgent{a2}, stores2)
	if fake.versions != 1 {
		t.Fatalf("switching store must republish, got %d", fake.versions)
	}
	if fake.storeCreates != 2 {
		t.Fatalf("expected mem-2 provisioned, got storeCreates=%d", fake.storeCreates)
	}
}

// When a store can't be provisioned (e.g. its embedding model isn't deployed),
// the referencing agent is still created — just left unbound — rather than being
// blocked or pointed at a store that doesn't exist.
func TestReconcile_StoreProvisionFailureLeavesAgentUnbound(t *testing.T) {
	fake := newFake()
	fake.failStoreCreate = true
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)

	a := shared.DesiredAgent{
		AgentID: "a1", Name: "Support", Type: shared.AgentPrompt, Version: "v1", Model: "gpt-4o",
		Definition: shared.AgentDefinition{Instructions: "help", MemoryStore: "mem-1"},
	}
	stores := []shared.DesiredMemoryStore{{ID: "mem-1", Name: "Mem", Definition: shared.MemoryStoreDefinition{ChatModel: "gpt-4o", EmbeddingModel: "not-deployed"}}}

	st := reconcileAgents(f, context.Background(), []shared.DesiredAgent{a}, stores)
	if len(st) != 1 || st[0].Health != healthLive {
		t.Fatalf("agent should stay healthy even if its store can't provision: %+v", st)
	}
	if fake.creates != 1 {
		t.Fatalf("agent should still be created, got %d", fake.creates)
	}
	for _, td := range fake.items["a1"].Versions.Latest.Definition.Tools {
		if td.Type == memoryToolType {
			t.Fatalf("agent must not be bound to a store that failed to provision")
		}
	}
}
