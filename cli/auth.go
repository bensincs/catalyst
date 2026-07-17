package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

func authority(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant
}

/* ── login ──────────────────────────────────────────────────────────────── */

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	cfg := registerGlobal(fs)
	asSP := fs.Bool("service-principal", false, "sign in as a service principal (needs --client-id, --client-secret, --tenant)")
	device := fs.Bool("device-code", false, "use the device code flow instead of opening a browser")
	var secret string
	fs.StringVar(&secret, "client-secret", envOr("CORTEX_CLIENT_SECRET", ""), "service principal client secret")
	fs.StringVar(&secret, "p", envOr("CORTEX_CLIENT_SECRET", ""), "service principal client secret (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch {
	case *asSP || secret != "":
		return loginServicePrincipal(ctx, cfg, secret)
	case *device:
		return loginDeviceCode(ctx, cfg)
	default:
		return loginInteractive(ctx, cfg)
	}
}

func loginInteractive(ctx context.Context, cfg *config) error {
	client, err := publicClient(cfg)
	if err != nil {
		return err
	}
	fmt.Println("Opening your browser to sign in …")
	res, err := client.AcquireTokenInteractive(ctx, cfg.delegatedScope())
	if err != nil {
		return fmt.Errorf("browser sign-in failed: %w", err)
	}
	fmt.Printf("Signed in as %s.\n", res.Account.PreferredUsername)
	return nil
}

func loginDeviceCode(ctx context.Context, cfg *config) error {
	client, err := publicClient(cfg)
	if err != nil {
		return err
	}
	dc, err := client.AcquireTokenByDeviceCode(ctx, cfg.delegatedScope())
	if err != nil {
		return err
	}
	fmt.Println(dc.Result.Message)
	res, err := dc.AuthenticationResult(ctx)
	if err != nil {
		return fmt.Errorf("device sign-in failed: %w", err)
	}
	fmt.Printf("Signed in as %s.\n", res.Account.PreferredUsername)
	return nil
}

func loginServicePrincipal(ctx context.Context, cfg *config, secret string) error {
	if secret == "" {
		return fmt.Errorf("--client-secret is required for --service-principal")
	}
	if cfg.clientID == "" || cfg.clientID == defClientID {
		return fmt.Errorf("--client-id (the service principal's app id) is required for --service-principal")
	}
	if cfg.tenant == "" || cfg.tenant == defTenant {
		return fmt.Errorf("--tenant (the service principal's tenant) is required for --service-principal")
	}
	if _, err := servicePrincipalToken(ctx, &spEntry{
		ClientID: cfg.clientID, ClientSecret: secret, Tenant: cfg.tenant, APIApp: cfg.apiApp,
	}); err != nil {
		return fmt.Errorf("service principal sign-in failed: %w", err)
	}
	p, err := spPath()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(spEntry{
		ClientID: cfg.clientID, ClientSecret: secret, Tenant: cfg.tenant, APIApp: cfg.apiApp,
	}, "", "  ")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	fmt.Printf("Signed in as service principal %s (tenant %s).\n", cfg.clientID, cfg.tenant)
	return nil
}

/* ── logout ─────────────────────────────────────────────────────────────── */

func cmdLogout(args []string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	removed := false
	for _, f := range []string{"token-cache.json", "sp.json"} {
		if os.Remove(filepath.Join(dir, f)) == nil {
			removed = true
		}
	}
	if removed {
		fmt.Println("Signed out.")
	} else {
		fmt.Println("Nothing to sign out of.")
	}
	return nil
}

/* ── token acquisition (used by create / whoami) ────────────────────────── */

// acquireToken returns a bearer token for the API: a service principal (env or
// stored) if one is configured, otherwise the cached user session (refreshed
// silently). If neither is available it asks the caller to sign in.
func acquireToken(ctx context.Context, cfg *config) (string, error) {
	if sp := loadSP(); sp != nil {
		return servicePrincipalToken(ctx, sp)
	}
	client, err := publicClient(cfg)
	if err != nil {
		return "", err
	}
	accts, err := client.Accounts(ctx)
	if err != nil {
		return "", err
	}
	if len(accts) == 0 {
		return "", fmt.Errorf("not signed in — run 'cortexctl login'")
	}
	res, err := client.AcquireTokenSilent(ctx, cfg.delegatedScope(), public.WithSilentAccount(accts[0]))
	if err != nil {
		return "", fmt.Errorf("session expired — run 'cortexctl login' again")
	}
	return res.AccessToken, nil
}

func servicePrincipalToken(ctx context.Context, sp *spEntry) (string, error) {
	cred, err := confidential.NewCredFromSecret(sp.ClientSecret)
	if err != nil {
		return "", err
	}
	client, err := confidential.New(authority(sp.Tenant), sp.ClientID, cred)
	if err != nil {
		return "", err
	}
	res, err := client.AcquireTokenByCredential(ctx, []string{"api://" + sp.APIApp + "/.default"})
	if err != nil {
		return "", err
	}
	return res.AccessToken, nil
}

func publicClient(cfg *config) (public.Client, error) {
	cp, err := cachePath()
	if err != nil {
		return public.Client{}, err
	}
	return public.New(cfg.clientID,
		public.WithAuthority(authority(cfg.tenant)),
		public.WithCache(&fileCache{path: cp}),
	)
}

/* ── persistence ────────────────────────────────────────────────────────── */

// fileCache persists the MSAL token cache (with the refresh token) to disk so a
// browser/device sign-in survives between invocations.
type fileCache struct{ path string }

func (fc *fileCache) Replace(ctx context.Context, u cache.Unmarshaler, _ cache.ReplaceHints) error {
	data, err := os.ReadFile(fc.path)
	if err != nil {
		return nil // absent cache is not an error
	}
	return u.Unmarshal(data)
}

func (fc *fileCache) Export(ctx context.Context, m cache.Marshaler, _ cache.ExportHints) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(fc.path, data, 0o600)
}

func cachePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token-cache.json"), nil
}

type spEntry struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Tenant       string `json:"tenant"`
	APIApp       string `json:"apiApp"`
}

func spPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sp.json"), nil
}

// loadSP resolves a service principal from the environment (CORTEX_CLIENT_ID +
// CORTEX_CLIENT_SECRET) first, then the file written by `login --service-principal`.
func loadSP() *spEntry {
	if id := strings.TrimSpace(os.Getenv("CORTEX_CLIENT_ID")); id != "" {
		if sec := strings.TrimSpace(os.Getenv("CORTEX_CLIENT_SECRET")); sec != "" {
			return &spEntry{ClientID: id, ClientSecret: sec, Tenant: envOr("CORTEX_TENANT", defTenant), APIApp: envOr("CORTEX_API_APP_ID", defAPIAppID)}
		}
	}
	p, err := spPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var sp spEntry
	if json.Unmarshal(data, &sp) != nil || sp.ClientID == "" || sp.ClientSecret == "" {
		return nil
	}
	return &sp
}
