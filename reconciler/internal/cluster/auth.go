package cluster

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/inception42/cortex/shared"
)

// Application Gateway for Containers has no OIDC and no external-authorization
// hook — its whole policy surface is load balancing, TLS, health checks, mTLS,
// routing and WAF. So a protected app cannot be guarded by a gateway policy, and
// the "one shared auth service" pattern (nginx auth_request / Envoy ext_authz)
// is unavailable too.
//
// Instead oauth2-proxy sits IN the request path, one per protected app:
//
//	AGC ──HTTPRoute(app host)──▶ oauth2-proxy ──▶ app Service
//
// The cost is two extra pods per protected app, and a redirect URI per app for
// the customer to register — both inherent to doing OIDC on AGC.
const (
	authPortName = "http"
	authPort     = 4180
	// proxyPrefix is oauth2-proxy's own path space. The callback lives beneath
	// it, which is what makes the redirect URI predictable per app.
	proxyPrefix = "/oauth2"
)

// authName is the in-cluster name of an app's oauth2-proxy objects. Derived from
// the app name so it is stable and collision-free within the namespace.
func authName(appName string) string { return appName + "-auth" }

// authRedirectURL is the OAuth callback for an app — what the customer must
// register as a redirect URI on their app registration.
func authRedirectURL(host string) string {
	return "https://" + host + proxyPrefix + "/callback"
}

// cookieSecretFor derives oauth2-proxy's cookie-encryption key.
//
// Deterministic rather than random on purpose: the reconciler is level-triggered
// and re-applies every sweep, so a fresh random value would rotate the key
// constantly and log every user out. Derived from the tenant slug, the app and
// the OIDC client secret, so it is stable, unguessable without the client
// secret, and changes if that secret is rotated.
//
// oauth2-proxy requires exactly 16, 24 or 32 bytes; base64url of a 32-byte hash
// is what it expects for AES-256.
func cookieSecretFor(tenantSlug, appID, clientSecret string) string {
	sum := sha256.Sum256([]byte("cortex-oauth2-cookie|" + tenantSlug + "|" + appID + "|" + clientSecret))
	return base64.URLEncoding.EncodeToString(sum[:])[:43] // 32 bytes, unpadded
}

// authSecret holds the app's oauth2-proxy credentials: the customer's OIDC
// client secret (delivered by the control plane) and the derived cookie key.
func authSecret(name, namespace, appID, clientSecret, cookieSecret string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{labelManaged: "true", labelAppID: appID},
		},
		"type": "Opaque",
		"stringData": map[string]any{
			"client-secret": clientSecret,
			"cookie-secret": cookieSecret,
		},
	}}
}

// authDeployment renders the oauth2-proxy that fronts one app.
func authDeployment(name, namespace string, a shared.DesiredApplication, ing *shared.IngressConfig, host string) *unstructured.Unstructured {
	upstream := fmt.Sprintf("http://%s:%d", a.ExposeService, exposePortOr80(a.ExposePort))

	// openid is mandatory for OIDC; profile/email populate the identity the
	// upstream sees. The app's own scope is appended so a token minted for one
	// app is not automatically good for another.
	scopes := []string{"openid", "profile", "email"}
	if s := strings.TrimSpace(a.OIDCScope); s != "" {
		scopes = append(scopes, s)
	}

	args := []any{
		"--provider=oidc",
		"--oidc-issuer-url=" + ing.OIDCIssuer,
		"--client-id=" + ing.OIDCClientID,
		"--redirect-url=" + authRedirectURL(host),
		"--scope=" + strings.Join(scopes, " "),
		// oauth2-proxy resolves claims it cannot find in the ID token by calling
		// the provider's profile (userinfo) endpoint. That can never work here:
		// the access token is minted for the app's own API (a.OIDCScope), not
		// for Graph, so Graph rejects it with a 401 and the callback fails. The
		// fallback has to be off entirely rather than fixed per claim — it is
		// reached by `email`, `groups`, and anything else not in the token.
		"--skip-claims-from-profile-url=true",
		// With no fallback, the email must be a claim Entra actually issues.
		// v2.0 omits `email` unless a directory admin adds it as an optional
		// claim, and many work accounts have no mail attribute to put there.
		// `preferred_username` is always present when `profile` is requested.
		"--oidc-email-claim=preferred_username",
		"--upstream=" + upstream,
		fmt.Sprintf("--http-address=0.0.0.0:%d", authPort),
		// Behind AGC, so trust forwarded headers — but only from the gateway.
		// Left unset, oauth2-proxy trusts every source IP for X-Forwarded-*,
		// which would let a caller spoof them.
		"--reverse-proxy=true",
		"--real-client-ip-header=X-Forwarded-For",
		// Any authenticated user in the tenant's directory; per-app authorization
		// is the scope above.
		"--email-domain=*",
		// Skip the interstitial "Sign in with…" page and go straight to Entra.
		"--skip-provider-button=true",
		// PKCE as defence in depth on top of the confidential client.
		"--code-challenge-method=S256",
		// The cookie must only ever travel over TLS; the gateway terminates HTTPS.
		"--cookie-secure=true",
		"--cookie-httponly=true",
		"--cookie-samesite=lax",
		// Identity for the upstream app.
		"--pass-basic-auth=true",
		"--pass-user-headers=true",
		"--set-xauthrequest=true",
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{labelManaged: "true", labelAppID: a.ID},
		},
		"spec": map[string]any{
			"replicas": int64(2),
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{"app": name, labelManaged: "true", labelAppID: a.ID},
					// Roll the pods when the credentials change; without this a
					// rotated client secret would be ignored until restart.
					"annotations": map[string]any{
						"cortex.io/credentials-hash": shortHash(ing.OIDCClientID + "|" + ing.OIDCClientSecret + "|" + host),
					},
				},
				"spec": map[string]any{
					"containers": []any{map[string]any{
						"name":  "oauth2-proxy",
						"image": oauth2ProxyImage,
						"args":  args,
						"env": []any{
							secretEnv("OAUTH2_PROXY_CLIENT_SECRET", name, "client-secret"),
							secretEnv("OAUTH2_PROXY_COOKIE_SECRET", name, "cookie-secret"),
						},
						"ports": []any{map[string]any{"name": authPortName, "containerPort": int64(authPort)}},
						"readinessProbe": map[string]any{
							"httpGet":             map[string]any{"path": "/ready", "port": int64(authPort)},
							"initialDelaySeconds": int64(3),
							"periodSeconds":       int64(10),
						},
						"resources": map[string]any{
							"requests": map[string]any{"cpu": "20m", "memory": "64Mi"},
							"limits":   map[string]any{"memory": "128Mi"},
						},
					}},
				},
			},
		},
	}}
}

// authService is what the app's HTTPRoute targets instead of the app itself.
func authService(name, namespace, appID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{labelManaged: "true", labelAppID: appID},
		},
		"spec": map[string]any{
			"selector": map[string]any{"app": name},
			"ports": []any{map[string]any{
				"name":       authPortName,
				"port":       int64(80),
				"targetPort": int64(authPort),
			}},
		},
	}}
}

func secretEnv(name, secretName, key string) map[string]any {
	return map[string]any{
		"name": name,
		"valueFrom": map[string]any{
			"secretKeyRef": map[string]any{"name": secretName, "key": key},
		},
	}
}

func exposePortOr80(p int) int {
	if p <= 0 {
		return 80
	}
	return p
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
