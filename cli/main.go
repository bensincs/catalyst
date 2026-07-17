// Command cortexctl is a small client for the Cortex control plane. Its only
// mutating verb is create: it POSTs a batch of resources — defined in one JSON
// file — to the generic /api/resources endpoint. Auth mirrors the Azure CLI:
// interactive browser sign-in (default), device code, or a service principal
// (client id + secret).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Defaults target the Cortex platform. Every one is overridable by flag or env
// so the same binary drives any environment.
const (
	defAPIURL   = "https://api.catalyst.msft.ae"
	defAPIAppID = "33e1686e-d227-454a-9974-4978c567720b"           // the control-plane API app registration
	defClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"           // Azure CLI public client (pre-consented to the API scope)
	defTenant   = "organizations"                                  // any work/school tenant; pin with --tenant
)

type config struct {
	apiURL   string
	apiApp   string
	clientID string
	tenant   string
}

// delegatedScope is the user (browser/device) scope; appScope is the service-
// principal (.default) scope.
func (c *config) delegatedScope() []string { return []string{"api://" + c.apiApp + "/access_as_user"} }
func (c *config) appScope() []string       { return []string{"api://" + c.apiApp + "/.default"} }

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// registerGlobal wires the shared connection flags onto a subcommand's flag set,
// seeded from env then built-in defaults.
func registerGlobal(fs *flag.FlagSet) *config {
	c := &config{}
	fs.StringVar(&c.apiURL, "api-url", envOr("CORTEX_API_URL", defAPIURL), "control-plane base URL")
	fs.StringVar(&c.apiApp, "api-app-id", envOr("CORTEX_API_APP_ID", defAPIAppID), "API app registration id (audience)")
	fs.StringVar(&c.clientID, "client-id", envOr("CORTEX_CLIENT_ID", defClientID), "public client id for interactive/device sign-in")
	fs.StringVar(&c.tenant, "tenant", envOr("CORTEX_TENANT", defTenant), "Entra tenant id or 'organizations'")
	return c
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "login":
		err = cmdLogin(os.Args[2:])
	case "logout":
		err = cmdLogout(os.Args[2:])
	case "create", "apply":
		err = cmdCreate(os.Args[2:])
	case "whoami":
		err = cmdWhoami(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "cortexctl: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cortexctl: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cortexctl — create Cortex resources from a JSON file.

Usage:
  cortexctl login [--device-code | --service-principal --client-id <id> --client-secret <secret> --tenant <tenant>]
  cortexctl create -f <resources.json>
  cortexctl whoami
  cortexctl logout

The resources file is the batch the control plane's POST /api/resources accepts:

  {
    "infrastructure": [ { "name": "kv", "bicepModule": "br/public:avm/res/key-vault/vault:0.13.3", "bicepParams": {}, "dependencies": [] } ],
    "memoryStores":   [ { "name": "notes", "description": "", "definition": {} } ],
    "agents":         [ { "name": "Reviewer", "type": "prompt", "model": "gpt-4o", "definition": { "instructions": "..." } } ],
    "applications":   [ { "name": "web", "repoURL": "https://…", "chart": "nginx", "namespace": "web", "dependencies": [] } ]
  }

Any subset of the four kinds is allowed; the whole batch is created in one
transaction, in dependency order. Common flags: --api-url, --tenant, --client-id.
Env: CORTEX_API_URL, CORTEX_TENANT, CORTEX_CLIENT_ID, CORTEX_CLIENT_SECRET.
`)
}

/* ── create ─────────────────────────────────────────────────────────────── */

// batch mirrors the control plane's ApplyBatch: any subset of the four kinds.
// Items stay raw so the file is passed through verbatim (the API is the schema).
type batch struct {
	Infrastructure []json.RawMessage `json:"infrastructure,omitempty"`
	MemoryStores   []json.RawMessage `json:"memoryStores,omitempty"`
	Agents         []json.RawMessage `json:"agents,omitempty"`
	Applications   []json.RawMessage `json:"applications,omitempty"`
}

func (b batch) count() int {
	return len(b.Infrastructure) + len(b.MemoryStores) + len(b.Agents) + len(b.Applications)
}

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	cfg := registerGlobal(fs)
	file := fs.String("f", "", "path to the resources JSON file ('-' for stdin)")
	fs.StringVar(file, "file", "", "path to the resources JSON file ('-' for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*file) == "" {
		return fmt.Errorf("a resources file is required: cortexctl create -f resources.json")
	}

	raw, err := readInput(*file)
	if err != nil {
		return err
	}
	var b batch
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("resources file is not valid JSON: %w", err)
	}
	if b.count() == 0 {
		return fmt.Errorf("nothing to create — the file has no infrastructure, memoryStores, agents, or applications")
	}
	// Re-marshal the recognised shape so a stray top-level key can't be sent.
	body, err := json.Marshal(b)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	token, err := acquireToken(ctx, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Creating %d resource(s) via %s …\n", b.count(), cfg.apiURL)
	res, err := postJSON(ctx, cfg.apiURL+"/api/resources", token, body)
	if err != nil {
		return err
	}

	// The endpoint returns the created ids by kind; echo them, else the raw body.
	var out struct {
		Infrastructure []string `json:"infrastructure"`
		MemoryStores   []string `json:"memoryStores"`
		Agents         []string `json:"agents"`
		Applications   []string `json:"applications"`
	}
	if json.Unmarshal(res, &out) == nil {
		printCreated("Infrastructure", out.Infrastructure)
		printCreated("Memory stores", out.MemoryStores)
		printCreated("Agents", out.Agents)
		printCreated("Applications", out.Applications)
	}
	fmt.Println("Done.")
	return nil
}

func printCreated(label string, ids []string) {
	for _, id := range ids {
		fmt.Printf("  ✓ %-14s %s\n", label, id)
	}
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

/* ── whoami ─────────────────────────────────────────────────────────────── */

func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	cfg := registerGlobal(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	token, err := acquireToken(ctx, cfg)
	if err != nil {
		return err
	}
	res, err := getJSON(ctx, cfg.apiURL+"/api/me", token)
	if err != nil {
		return err
	}
	var me struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
		TID   string `json:"tid"`
	}
	if json.Unmarshal(res, &me) == nil && me.Email != "" {
		fmt.Printf("%s (%s)\nrole: %s\ntenant: %s\n", me.Name, me.Email, me.Role, me.TID)
		return nil
	}
	fmt.Println(string(res))
	return nil
}

/* ── HTTP ───────────────────────────────────────────────────────────────── */

var httpClient = &http.Client{Timeout: 90 * time.Second}

func postJSON(ctx context.Context, url, token string, body []byte) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return doHTTP(req)
}

func getJSON(ctx context.Context, url, token string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return doHTTP(req)
}

func doHTTP(req *http.Request) ([]byte, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if m := parseAPIError(data); m != "" {
			msg = m
		}
		return nil, fmt.Errorf("%s %s → %d: %s", req.Method, req.URL.Path, resp.StatusCode, msg)
	}
	return data, nil
}

func parseAPIError(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		return e.Error
	}
	return ""
}
