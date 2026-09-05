package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Key in the auth Secret holding the session store's password.
const sessionPasswordKey = "session-password"

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
func authSecret(name, namespace, appID, clientSecret, cookieSecret, sessionPassword string) *unstructured.Unstructured {
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
			"client-secret":    clientSecret,
			"cookie-secret":    cookieSecret,
			sessionPasswordKey: sessionPassword,
		},
	}}
}

// authDeployment renders the oauth2-proxy that fronts one app.
func authDeployment(name, namespace string, a shared.DesiredApplication, ing *shared.IngressConfig, host string) *unstructured.Unstructured {
	// Not the app: the authorization hop in this same pod, which decides whether
	// this caller may reach the app and stamps the identity headers the app
	// trusts. Over localhost, so the decision cannot be skipped by anything that
	// can reach the pod's Service.
	upstream := fmt.Sprintf("http://127.0.0.1:%d", authzHopPort)

	// openid is mandatory for OIDC; profile/email populate the identity the
	// upstream sees. The app's own scope is appended so a token minted for one
	// app is not automatically good for another.
	// offline_access asks Entra for a refresh token. Without one oauth2-proxy
	// cannot renew the ID token it forwards, and the session outlives it: the
	// cookie stays valid while the token behind it expires after about an hour,
	// so the proxy waves the request through and the hop behind it rejects a
	// stale token. Every signed-in user hits that, an hour in.
	scopes := []string{"openid", "profile", "email", "offline_access"}
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
		//
		// The Authorization header carries the OIDC ID token as a Bearer JWT,
		// because that is what an application behind a gateway expects to
		// receive. --pass-basic-auth is deliberately OFF: it puts
		// "Authorization: Basic <user:password>" on the request instead, which
		// does not merely fail to help — it occupies the one header a JWT-
		// validating upstream reads, so the app sees a credential it cannot
		// parse and reports the session as invalid.
		// Renew the session well before the ID token expires, rather than
		// discovering it has by being refused downstream.
		"--cookie-refresh=15m",
		"--cookie-expire=8h",
		// Keep the session here, not in the browser. It does not fit in a
		// cookie — see sessionStoreImage — and a split cookie loses the refresh
		// token, which is what ends a session an hour after signing in.
		"--session-store-type=redis",
		"--redis-connection-url=" + sessionStoreURL(a.Name, namespace),
		"--pass-authorization-header=true",
		"--pass-basic-auth=false",
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
							// Supplied here rather than in the connection URL, which
							// would put the password in an argument list that shows
							// up in kubectl describe and every crash report.
							secretEnv("OAUTH2_PROXY_REDIS_PASSWORD", name, sessionPasswordKey),
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
					}, map[string]any{
						"name":  "authz-hop",
						"image": authzHopImage,
						"args":  []any{"serve", "proxy", "--config", "/etc/oathkeeper/config.yaml"},
						"ports": []any{map[string]any{"name": "authz", "containerPort": int64(authzHopPort)}},
						"volumeMounts": []any{map[string]any{
							"name":      "authz-hop-config",
							"mountPath": "/etc/oathkeeper",
							"readOnly":  true,
						}},
						"resources": map[string]any{
							"requests": map[string]any{"cpu": "20m", "memory": "64Mi"},
							"limits":   map[string]any{"memory": "128Mi"},
						},
					}},
					"volumes": []any{map[string]any{
						"name":      "authz-hop-config",
						"configMap": map[string]any{"name": name + "-authz"},
					}},
				},
			},
		},
	}}
}

// ─── Authorization hop ───────────────────────────────────────────────────────
//
// oauth2-proxy establishes WHO the caller is. It cannot decide WHETHER they may
// reach this app — it has no external-authorization hook — and Application
// Gateway for Containers has none either. So Ory Oathkeeper sits between the
// two, in the same pod, and asks the platform's authorization service per
// request:
//
//	AGC ──▶ oauth2-proxy ──localhost──▶ Oathkeeper ──▶ app Service
//	                                        │
//	                                        └── /v1/authz/decide
//
// It also STAMPS the identity headers the app trusts. That is the part nothing
// else in this path can do, and the reason an identity-aware proxy is here at
// all rather than a plain reverse proxy.
const (
	// Oathkeeper's proxy port. Only oauth2-proxy talks to it, over localhost,
	// so the decision cannot be skipped by anything that can reach the Service.
	authzHopPort = 4455
	// Where the platform's authorization service lives, when the tenant has not
	// said otherwise. A default rather than a constant: which service decides
	// access is the platform operator's choice, and a generic component should
	// not have one service's address compiled into it.
	defaultAuthzDecideURL = "http://authz-service.cortex-authz.svc.cluster.local:8080/v1/authz/decide"
	authzHopImage         = "docker.io/oryd/oathkeeper:v0.40.7"
	// Where a hook's credential goes when it does not say. Deliberately not
	// Authorization: the API server's proxy consumes that header for its own
	// authentication and never forwards it, so a subscriber would see no
	// credential at all and answer 401.
	defaultHookTokenHeader = "X-Cortex-Hook-Token"
	// Placeholder a hook uses to ask about each of an application's roles.
	roleToken = "{{role}}"
	// Used when an application declares no roles of its own, so it is still
	// grantable rather than invisible to whoever manages access.
	defaultRole = "user"
)

// subjectExpr is how the caller is identified, everywhere.
//
// Not .Subject: for the jwt authenticator that is the token's `sub`, and
// Entra's `sub` is an opaque pairwise identifier — different per application
// and meaningless to a human — while grants are written against the address an
// operator types into the admin UI. Nothing would ever match what was granted.
//
// The authenticator exposes the token's claims on .Extra, and preferred_username
// is the address the token carries. (`subject_from` is not a property of this
// authenticator; setting it makes Oathkeeper reject the entire rule and answer
// every request with a 500.)
const subjectExpr = `{{ print .Extra.preferred_username }}`

// identityHeaders are the headers a downstream application trusts to say who
// the caller is.
//
// Every one is overwritten on the way through, whether or not there is a value
// for it. They are a trust boundary, and an application reading them cannot
// tell a header the platform set from one the caller typed — Insight, for
// instance, treats X-Cortex-Sub as proof of identity. A proxy that only ADDS
// the headers it knows about forwards the rest verbatim, so a caller could
// assert any identity they liked. Envoy's ext_authz replaces same-named headers
// for exactly this reason; here it has to be deliberate.
//
// The value is the empty string for headers this hop does not populate, which
// removes them.
func identityHeaders(sub, tenant, app string) map[string]any {
	return map[string]any{
		"X-Cortex-Sub":                      sub,
		"X-Cortex-Subject":                  sub,
		"X-Cortex-Tenant":                   tenant,
		"X-Cortex-App":                      app,
		"X-Cortex-Email":                    "",
		"X-Cortex-Name":                     "",
		"X-Cortex-Roles":                    "",
		"X-Cortex-Claim-Preferred-Username": "",
		"X-Cortex-Claim-Given-Name":         "",
		"X-Cortex-Claim-Family-Name":        "",
	}
}

// jwksURLFor derives the signing-key endpoint from an OIDC issuer.
//
// Entra's issuer already ends in /v2.0 while its JWKS lives at
// /discovery/v2.0/keys off the TENANT root — so appending the well-known suffix
// to the issuer yields /v2.0/discovery/v2.0/keys, which 404s. Every request
// would then fail to authenticate, with nothing in the proxy's logs naming a
// malformed URL as the reason.
func jwksURLFor(issuer string) string {
	root := strings.TrimSuffix(strings.TrimSuffix(issuer, "/"), "/v2.0")
	return root + "/discovery/v2.0/keys"
}

// authzHopConfig renders Oathkeeper's own configuration.
//
// A handler enabled here must also carry its required config block, even though
// the access rule overrides every value — Oathkeeper validates the global
// section against its schema at startup and exits if a required property is
// missing. These values are therefore duplicated from the rule rather than left
// out, and an incomplete global section fails the container at boot rather than
// at the first request.
func authzHopConfig(appName, tenantSlug, decideURL string, ing *shared.IngressConfig) string {
	jwks := jwksURLFor(ing.OIDCIssuer)
	headers, _ := json.Marshal(identityHeaders(subjectExpr, tenantSlug, appName))
	payload := `{"subject":"` + subjectExpr + `","app":"` + appName + `"}`

	return `log:
  level: warn
  format: json
serve:
  proxy:
    port: ` + fmt.Sprint(authzHopPort) + `
access_rules:
  repositories:
    - file:///etc/oathkeeper/rules.json
errors:
  fallback:
    - json
  handlers:
    json:
      enabled: true
authenticators:
  jwt:
    enabled: true
    config:
      jwks_urls:
        - ` + jwks + `
  noop:
    enabled: true
  anonymous:
    enabled: true
authorizers:
  remote_json:
    enabled: true
    config:
      remote: ` + decideURL + `
      payload: '` + payload + `'
  allow:
    enabled: true
mutators:
  header:
    enabled: true
    config:
      headers: ` + string(headers) + `
  noop:
    enabled: true
`
}

// authzHopRules renders the single access rule that fronts one app.
//
// The authenticator is `jwt`: oauth2-proxy has already completed the OIDC
// exchange and forwards the ID token, so the caller's identity is provable here
// rather than merely asserted. Trusting the upstream blindly would mean anything
// that reached this port was whoever it claimed to be.
func authzHopRules(appName, tenantSlug, upstream, decideURL string, ing *shared.IngressConfig) string {
	jwks := jwksURLFor(ing.OIDCIssuer)

	rule := map[string]any{
		"id": "cortex-" + appName,
		"match": map[string]any{
			"url":     "<http|https>://<.*>/<.*>",
			"methods": []any{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		},
		"authenticators": []any{map[string]any{
			"handler": "jwt",
			"config": map[string]any{
				"jwks_urls":          []any{jwks},
				"trusted_issuers":    []any{ing.OIDCIssuer},
				"target_audience":    []any{ing.OIDCClientID},
				"allowed_algorithms": []any{"RS256"},
			},
		}},
		"authorizer": map[string]any{
			"handler": "remote_json",
			"config": map[string]any{
				"remote": decideURL,
				// Subject comes from the verified token, never from the request.
				"payload": `{"subject":"` + subjectExpr + `","app":"` + appName + `"}`,
			},
		},
		"mutators": []any{map[string]any{
			"handler": "header",
			"config":  map[string]any{"headers": identityHeaders(subjectExpr, tenantSlug, appName)},
		}},
		"upstream": map[string]any{"url": upstream},
	}
	b, _ := json.Marshal([]any{rule})
	return string(b)
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

// authzHopConfigMap carries Oathkeeper's configuration and its single access
// rule into the pod.
func authzHopConfigMap(name, namespace, appID, tenantSlug, decideURL string, a shared.DesiredApplication, ing *shared.IngressConfig) *unstructured.Unstructured {
	upstream := fmt.Sprintf("http://%s:%d", a.ExposeService, exposePortOr80(a.ExposePort))
	if strings.TrimSpace(decideURL) == "" {
		decideURL = defaultAuthzDecideURL
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{labelManaged: "true", labelAppID: appID},
		},
		"data": map[string]any{
			"config.yaml": authzHopConfig(appNameForAuthz(a), tenantSlug, decideURL, ing),
			"rules.json":  authzHopRules(appNameForAuthz(a), tenantSlug, upstream, decideURL, ing),
		},
	}}
}

// appNameForAuthz is the name this app is known by to the authorization
// service. It is the app's hostname label, which is what an operator sees and
// what role grants are written against — not the control plane's internal id.
func appNameForAuthz(a shared.DesiredApplication) string {
	if h := strings.TrimSpace(a.Hostname); h != "" {
		return strings.ToLower(h)
	}
	return strings.ToLower(strings.TrimSpace(a.ID))
}

// ─── Application hooks ───────────────────────────────────────────────────────

// runApplicationHooks tells every subscriber that an application was deployed.
//
// The reconciler does not know what a hook does. That is the point: a service
// which must hear about apps declares it in its own registration, instead of a
// generic component carrying that service's namespace, secret name, DNS name
// and API shape.
//
// Failures are reported per hook and do not stop the others, nor the app. The
// subscriber may simply not be installed yet, and the sweep is level-triggered,
// so the next one tries again.
func (k *kube) runApplicationHooks(ctx context.Context, hooks []shared.ApplicationHook, appName string, roles []string) []error {
	var errs []error
	for _, h := range hooks {
		// A hook that mentions {{role}} is asking about the application's
		// vocabulary, so it is called once per role. The application declares
		// those names itself and says nothing about who consumes them.
		for _, role := range hookRoles(h, roles) {
			if err := k.runApplicationHook(ctx, h, appName, role); err != nil {
				errs = append(errs, fmt.Errorf("%s (%s): %w", h.Name, role, err))
			}
		}
	}
	return errs
}

// hookRoles is what a hook should be called for.
//
// One empty entry when the hook does not mention a role — it is about the
// application, not its vocabulary. Otherwise the roles the application
// declares, falling back to a single default so an application that declares
// none is still reachable rather than silently ungrantable.
func hookRoles(h shared.ApplicationHook, declared []string) []string {
	if !strings.Contains(h.Path, roleToken) && !strings.Contains(h.Body, roleToken) {
		return []string{""}
	}
	var out []string
	for _, r := range declared {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return []string{defaultRole}
	}
	return out
}

func (k *kube) runApplicationHook(ctx context.Context, h shared.ApplicationHook, appName, role string) error {
	method := strings.TrimSpace(h.Method)
	if method == "" {
		method = http.MethodPost
	}
	if h.Service.Name == "" || h.Service.Namespace == "" {
		return errors.New("hook names no service to call")
	}

	path := strings.ReplaceAll(h.Path, "{{app}}", url.PathEscape(appName))
	path = strings.ReplaceAll(path, roleToken, url.PathEscape(role))
	body := strings.ReplaceAll(h.Body, "{{app}}", appName)
	body = strings.ReplaceAll(body, roleToken, role)

	headers := map[string]string{"Content-Type": "application/json"}
	if h.TokenSecret != nil {
		token, err := k.secretValue(ctx, *h.TokenSecret)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(h.TokenHeader)
		if name == "" {
			name = defaultHookTokenHeader
		}
		headers[name] = token
	}

	// Through the API server's proxy rather than the Service's cluster DNS
	// name: this runs outside the cluster, where that name does not resolve,
	// and the API server is the one thing it can already reach and authenticate
	// to. A subscriber therefore does not have to be publicly routable.
	port := h.Service.Port
	if port == 0 {
		port = 80
	}
	req := k.rest.Verb(method).
		Namespace(h.Service.Namespace).
		Resource("services").
		Name(fmt.Sprintf("%s:%d", h.Service.Name, port)).
		SubResource("proxy").
		Suffix(path)
	for name, value := range headers {
		req = req.SetHeader(name, value)
	}
	if body != "" {
		req = req.Body([]byte(body))
	}

	// A short timeout on purpose: this runs inside the reconcile sweep, and a
	// subscriber that is slow or absent must not hold up every other app's
	// reconciliation behind it.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := req.DoRaw(ctx); err != nil {
		return err
	}
	return nil
}

// secretValue reads one key of one Secret.
func (k *kube) secretValue(ctx context.Context, ref shared.SecretKeyRef) (string, error) {
	sec, err := k.dyn.Resource(secGVR).Namespace(ref.Namespace).
		Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	data, _, _ := unstructured.NestedStringMap(sec.Object, "data")
	raw, ok := data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no %q", ref.Namespace, ref.Name, ref.Key)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("decode %s/%s/%s: %w", ref.Namespace, ref.Name, ref.Key, err)
	}
	return string(decoded), nil
}

/* ── Login sessions ───────────────────────────────────────────────────────── */

// sessionStoreName is the Redis holding login sessions for one app's proxy.
func sessionStoreName(appName string) string { return authName(appName) + "-sessions" }

// sessionPasswordFor derives the store's password.
//
// Derived rather than generated so it is the same on every reconcile: a fresh
// random password each pass would rotate the store out from under every signed
// in person. It never leaves the cluster, and is the same shape as the cookie
// key beside it.
func sessionPasswordFor(tenantSlug, appID, clientSecret string) string {
	sum := sha256.Sum256([]byte("cortex-session-store|" + tenantSlug + "|" + appID + "|" + clientSecret))
	return base64.URLEncoding.EncodeToString(sum[:])[:43]
}

// sessionStoreURL is what oauth2-proxy connects to. The password is supplied
// separately, through the environment, so it is not written into an argument
// list that shows up in `kubectl describe` and every crash report.
func sessionStoreURL(appName, namespace string) string {
	return fmt.Sprintf("redis://%s.%s.svc.cluster.local:6379", sessionStoreName(appName), namespace)
}

// sessionStoreDeployment renders the Redis behind one app's login.
//
// One replica, and no persistence. A session is not durable state: it can
// always be rebuilt by signing in again, and the identity provider usually does
// that without the person seeing anything. Replicating or persisting it would
// buy little and cost a great deal more than it is worth.
//
// The cost of that choice is honest and small: if this pod restarts, everyone
// signed into this app signs in again.
func sessionStoreDeployment(appName, namespace, appID string, credentialsHash string) *unstructured.Unstructured {
	name := sessionStoreName(appName)
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{labelManaged: "true", labelAppID: appID},
		},
		"spec": map[string]any{
			"replicas": int64(1),
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			// Never two at once. They would not share the sessions they hold, so
			// during a rolling update a request could be answered by whichever
			// one did not have yours.
			"strategy": map[string]any{"type": "Recreate"},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{"app": name, labelManaged: "true", labelAppID: appID},
					"annotations": map[string]any{
						"cortex.io/credentials-hash": credentialsHash,
					},
				},
				"spec": map[string]any{
					"automountServiceAccountToken": false,
					"securityContext": map[string]any{
						"runAsNonRoot": true,
						"runAsUser":    int64(999),
						"runAsGroup":   int64(999),
						"fsGroup":      int64(999),
					},
					"containers": []any{map[string]any{
						"name":  "redis",
						"image": sessionStoreImage,
						"securityContext": map[string]any{
							"allowPrivilegeEscalation": false,
							"readOnlyRootFilesystem":   true,
							"capabilities":             map[string]any{"drop": []any{"ALL"}},
						},
						// Anything in the cluster can reach a ClusterIP, so this
						// asks for a password like any other service would.
						//
						// --save "" turns off snapshotting: with a read-only root
						// filesystem a snapshot cannot be written, and Redis stops
						// accepting writes when a background save fails — which
						// would refuse logins rather than lose a session.
						"args": []any{
							"redis-server",
							"--requirepass", "$(REDIS_PASSWORD)",
							"--save", "",
							"--appendonly", "no",
							// Sessions carry their own expiry, so evict those
							// before ever refusing a write for want of memory.
							"--maxmemory", "192mb",
							"--maxmemory-policy", "volatile-lru",
						},
						"env": []any{
							secretEnv("REDIS_PASSWORD", authName(appName), sessionPasswordKey),
						},
						"ports": []any{map[string]any{"name": "redis", "containerPort": int64(6379)}},
						"readinessProbe": map[string]any{
							"tcpSocket":           map[string]any{"port": int64(6379)},
							"initialDelaySeconds": int64(3),
							"periodSeconds":       int64(10),
						},
						"livenessProbe": map[string]any{
							"tcpSocket":           map[string]any{"port": int64(6379)},
							"initialDelaySeconds": int64(20),
							"periodSeconds":       int64(20),
							"failureThreshold":    int64(3),
						},
						"resources": map[string]any{
							"requests": map[string]any{"cpu": "10m", "memory": "32Mi"},
							"limits":   map[string]any{"memory": "256Mi"},
						},
					}},
				},
			},
		},
	}}
}

// sessionStoreService is how the proxy reaches the store.
func sessionStoreService(appName, namespace, appID string) *unstructured.Unstructured {
	name := sessionStoreName(appName)
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
				"name": "redis", "port": int64(6379), "targetPort": int64(6379),
			}},
		},
	}}
}
