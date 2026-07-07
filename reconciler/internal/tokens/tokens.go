// Package tokens obtains the bearer tokens the reconciler presents to the Cortex
// control plane and to the in-tenant Foundry Agent Service, using the workload's
// own Azure identity via the standard DefaultAzureCredential chain: a managed
// identity in Azure (Container Apps / App Service / VM — selected by
// AZURE_CLIENT_ID), or the developer's Azure CLI / a service principal locally.
// The control plane validates the resulting Entra token against Entra's JWKS and
// maps its tenant id (tid) to the tenant — there is no shared secret anywhere in
// the path. One credential is built once (NewCredential) and reused per scope
// (SourceFor).
package tokens

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// Source yields a bearer token valid for a given Entra scope.
type Source interface {
	Token(ctx context.Context) (string, error)
}

// NewCredential builds the DefaultAzureCredential chain once. Share the result
// across scopes (Cortex API, Foundry) via SourceFor so the reconciler probes for
// a managed identity / developer credential a single time.
func NewCredential() (azcore.TokenCredential, error) {
	return azidentity.NewDefaultAzureCredential(nil)
}

// SourceFor returns a token source for scope backed by cred. scope may be a bare
// resource ("api://<id>", "https://ai.azure.com") or already suffixed with
// "/.default"; the suffix is added when missing.
func SourceFor(cred azcore.TokenCredential, scope string) Source {
	scope = strings.TrimSpace(scope)
	if scope != "" && !strings.HasSuffix(scope, "/.default") {
		scope = strings.TrimRight(scope, "/") + "/.default"
	}
	return &azureSource{cred: cred, scopes: []string{scope}}
}

// NewSource builds a token source backed by a fresh DefaultAzureCredential. scope
// is the Cortex API scope (e.g. api://<client-id> or api://<client-id>/.default).
// A user-assigned managed identity is selected via the AZURE_CLIENT_ID env var,
// which DefaultAzureCredential reads directly.
func NewSource(scope string) (Source, error) {
	if strings.TrimSpace(scope) == "" {
		return nil, errors.New("CORTEX_API_SCOPE is required")
	}
	cred, err := NewCredential()
	if err != nil {
		return nil, err
	}
	return SourceFor(cred, scope), nil
}

type azureSource struct {
	cred   azcore.TokenCredential
	scopes []string

	mu  sync.Mutex
	tok azcore.AccessToken
}

func (s *azureSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok.Token != "" && time.Until(s.tok.ExpiresOn) > time.Minute {
		return s.tok.Token, nil
	}
	tok, err := s.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: s.scopes})
	if err != nil {
		return "", err
	}
	s.tok = tok
	return tok.Token, nil
}
