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

// fakeFoundry is an in-memory stand-in for the subset of the Foundry Agents API
// the reconciler drives: list, create, publish-version, delete.
type fakeFoundry struct {
	mu                         sync.Mutex
	items                      map[string]agent
	creates, versions, deletes int
	failList                   bool
}

func newFake() *fakeFoundry { return &fakeFoundry{items: map[string]agent{}} }

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
	return mux
}

func newClient(endpoint string) *Foundry {
	return New(config.Config{
		FoundryEndpoint:   endpoint,
		FoundryAPIVersion: "v1",
		FoundryProject:    "test",
	}, staticToken{})
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

	st := f.Reconcile(ctx, []shared.DesiredAgent{a})
	if len(st) != 1 || st[0].Health != healthHealthy || st[0].Version != "v1" {
		t.Fatalf("create: got %+v", st)
	}
	if fake.creates != 1 || fake.versions != 0 || fake.deletes != 0 {
		t.Fatalf("create counts: c=%d v=%d d=%d", fake.creates, fake.versions, fake.deletes)
	}

	if st := f.Reconcile(ctx, []shared.DesiredAgent{a}); st[0].Health != healthHealthy {
		t.Fatalf("noop health: %+v", st)
	}
	if fake.creates != 1 || fake.versions != 0 {
		t.Fatalf("noop must not write: c=%d v=%d", fake.creates, fake.versions)
	}

	a2 := a
	a2.Version = "v2"
	a2.Definition.Instructions = "help more"
	st = f.Reconcile(ctx, []shared.DesiredAgent{a2})
	if st[0].Health != healthHealthy || st[0].Version != "v2" {
		t.Fatalf("version status: %+v", st)
	}
	if fake.versions != 1 {
		t.Fatalf("expected 1 new version, got %d", fake.versions)
	}

	if st := f.Reconcile(ctx, nil); len(st) != 0 {
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
	st := f.Reconcile(context.Background(), []shared.DesiredAgent{h})
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
	st := f.Reconcile(context.Background(), []shared.DesiredAgent{a})
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

	_ = f.Reconcile(context.Background(), nil)
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
	def, skipped := f.definitionFor(d)

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
