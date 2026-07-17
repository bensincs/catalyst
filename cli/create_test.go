package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostJSONSendsBatchWithBearer(t *testing.T) {
	var gotAuth, gotCT, gotMethod string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCT, gotMethod = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"agents":["cli-test"]}`))
	}))
	defer srv.Close()

	body := []byte(`{"agents":[{"name":"CLI Test"}]}`)
	res, err := postJSON(context.Background(), srv.URL+"/api/resources", "tok123", body)
	if err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("body = %s", gotBody)
	}
	var out struct {
		Agents []string `json:"agents"`
	}
	if json.Unmarshal(res, &out); len(out.Agents) != 1 || out.Agents[0] != "cli-test" {
		t.Fatalf("response parse: %+v", out)
	}
}

func TestDoHTTPSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"a resource with that name already exists"}`))
	}))
	defer srv.Close()
	_, err := postJSON(context.Background(), srv.URL+"/api/resources", "t", []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected the API error surfaced, got %v", err)
	}
}

func TestBatchCountAndPassthrough(t *testing.T) {
	var b batch
	if err := json.Unmarshal([]byte(`{"agents":[{},{}],"infrastructure":[{}],"stray":1}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.count() != 3 {
		t.Fatalf("count = %d, want 3", b.count())
	}
	// Re-marshalling drops unrecognised top-level keys (only the four kinds ship).
	out, _ := json.Marshal(b)
	if strings.Contains(string(out), "stray") {
		t.Fatalf("stray key leaked into the request body: %s", out)
	}
}
