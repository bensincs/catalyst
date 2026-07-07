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

// fakeFoundry is an in-memory stand-in for the subset of the Foundry Agent
// Service (Assistants-compatible) API the reconciler drives.
type fakeFoundry struct {
	mu                        sync.Mutex
	seq                       int
	items                     map[string]assistant
	creates, updates, deletes int
	failList                  bool
}

func newFake() *fakeFoundry { return &fakeFoundry{items: map[string]assistant{}} }

func (s *fakeFoundry) store(id string, spec assistantSpec) assistant {
	a := assistant{
		ID: id, Model: spec.Model, Name: spec.Name, Instructions: spec.Instructions,
		Tools: spec.Tools, Temperature: spec.Temperature, Metadata: spec.Metadata,
	}
	s.items[id] = a
	return a
}

func (s *fakeFoundry) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/assistants", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if s.failList {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			data := make([]assistant, 0, len(s.items))
			for _, a := range s.items {
				data = append(data, a)
			}
			_ = json.NewEncoder(w).Encode(assistantList{Data: data, HasMore: false})
		case http.MethodPost: // create
			var spec assistantSpec
			_ = json.NewDecoder(r.Body).Decode(&spec)
			s.seq++
			s.creates++
			_ = json.NewEncoder(w).Encode(s.store("asst_"+strconv.Itoa(s.seq), spec))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/assistants/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/assistants/")
		switch r.Method {
		case http.MethodPost: // update
			var spec assistantSpec
			_ = json.NewDecoder(r.Body).Decode(&spec)
			s.updates++
			_ = json.NewEncoder(w).Encode(s.store(id, spec))
		case http.MethodDelete:
			delete(s.items, id)
			s.deletes++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "deleted": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func newClient(endpoint string) *Foundry {
	return New(config.Config{
		FoundryEndpoint:   endpoint,
		FoundryAPIVersion: "2025-05-01",
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
// update on a version/spec change, prune when de-provisioned.
func TestReconcile_CreateNoopUpdatePrune(t *testing.T) {
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
	if fake.creates != 1 || fake.updates != 0 || fake.deletes != 0 {
		t.Fatalf("create counts: c=%d u=%d d=%d", fake.creates, fake.updates, fake.deletes)
	}

	if st := f.Reconcile(ctx, []shared.DesiredAgent{a}); st[0].Health != healthHealthy {
		t.Fatalf("noop health: %+v", st)
	}
	if fake.creates != 1 || fake.updates != 0 {
		t.Fatalf("noop must not write: c=%d u=%d", fake.creates, fake.updates)
	}

	a2 := a
	a2.Version = "v2"
	a2.Definition.Instructions = "help more"
	st = f.Reconcile(ctx, []shared.DesiredAgent{a2})
	if st[0].Health != healthHealthy || st[0].Version != "v2" {
		t.Fatalf("update status: %+v", st)
	}
	if fake.updates != 1 {
		t.Fatalf("expected 1 update, got %d", fake.updates)
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
	if fake.creates != 0 || fake.updates != 0 || fake.deletes != 0 {
		t.Fatalf("must not mutate when list fails: c=%d u=%d d=%d", fake.creates, fake.updates, fake.deletes)
	}
}

// The reconciler owns only what it stamped: agents authored elsewhere in the
// project must survive a prune.
func TestReconcile_LeavesUnmanagedAgentsAlone(t *testing.T) {
	fake := newFake()
	fake.items["asst_ext"] = assistant{ID: "asst_ext", Model: "gpt-4o", Metadata: map[string]string{"owner": "someone-else"}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	f := newClient(srv.URL)

	_ = f.Reconcile(context.Background(), nil)
	if fake.deletes != 0 {
		t.Fatalf("must not delete unmanaged agents, got %d", fake.deletes)
	}
	if _, ok := fake.items["asst_ext"]; !ok {
		t.Fatalf("unmanaged agent was removed")
	}
}
