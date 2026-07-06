// Package tokens obtains the bearer token the reconciler presents to the Cortex
// control plane, using the workload's own Azure identity via the standard
// DefaultAzureCredential chain: a managed identity in Azure (Container Apps /
// App Service / VM — selected by AZURE_CLIENT_ID), or the developer's Azure CLI
// / a service principal locally. The control plane validates the resulting Entra
// token against Entra's JWKS and maps its tenant id (tid) to the tenant — there
// is no shared secret anywhere in the path.
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

// Source yields a bearer token valid for the Cortex control-plane API.
type Source interface {
	Token(ctx context.Context) (string, error)
}

// NewSource builds a token source backed by DefaultAzureCredential. scope is the
// Cortex API scope (e.g. api://<client-id> or api://<client-id>/.default). A
// user-assigned managed identity is selected via the AZURE_CLIENT_ID env var,
// which DefaultAzureCredential reads directly.
func NewSource(scope string) (Source, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, errors.New("CORTEX_API_SCOPE is required")
	}
	if !strings.HasSuffix(scope, "/.default") {
		scope = strings.TrimRight(scope, "/") + "/.default"
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	return &azureSource{cred: cred, scopes: []string{scope}}, nil
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
