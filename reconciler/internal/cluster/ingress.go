package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/inception42/cortex/shared"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// A standalone Envoy proxy is installed as the cluster's public ingress — a
// hardened LoadBalancer that terminates traffic and enforces the tenant's Entra
// identity with Envoy's native jwt_authn filter (no service mesh). It's rendered
// per tenant by the reconciler: the JWT providers come from the tenant's own
// issuer(s), and the whole thing fails closed (403) when no identity is set.
const (
	ingressNS     = "cortex-ingress"
	ingressName   = "cortex-ingress"
	ingressCMName = "cortex-ingress-config"

	// Pinned distroless Envoy (runs as non-root uid 101).
	envoyImage = "envoyproxy/envoy:distroless-v1.31.5"

	httpPort  = 8080
	httpsPort = 8443
	adminPort = 9901

	tlsMountPath = "/etc/envoy/tls"

	// Argo project that bounds tenant Helm apps (kept from the mesh era).
	projectTenants = "cortex-tenants"

	configHashAnnotation = "cortex.io/config-hash"
)

const kubeAPIServer = "https://kubernetes.default.svc"

// securityHeadersLua hardens responses leaving the ingress: transport-security
// headers are always enforced; content headers are only defaulted when the app
// didn't set its own; server-fingerprint headers are stripped.
const securityHeadersLua = `function envoy_on_response(handle)
  local h = handle:headers()
  h:replace("strict-transport-security", "max-age=63072000; includeSubDomains; preload")
  h:replace("x-content-type-options", "nosniff")
  if h:get("x-frame-options") == nil and h:get("content-security-policy") == nil then
    h:replace("x-frame-options", "SAMEORIGIN")
  end
  if h:get("referrer-policy") == nil then
    h:replace("referrer-policy", "strict-origin-when-cross-origin")
  end
  h:remove("server")
  h:remove("x-powered-by")
end`

// validAuthRules keeps only fully-formed JWT rules: a rule missing an issuer,
// JWKS URI, or audience would weaken the ingress, so we drop it rather than
// apply it.
func validAuthRules(auth *shared.IngressAuth) []shared.IngressJWTRule {
	if auth == nil {
		return nil
	}
	out := make([]shared.IngressJWTRule, 0, len(auth.Rules))
	for _, r := range auth.Rules {
		if strings.TrimSpace(r.Issuer) == "" || strings.TrimSpace(r.JWKSURI) == "" || len(r.Audiences) == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

/* ── Envoy bootstrap config ────────────────────────────────────────────────── */

// envoyConfig renders the Envoy bootstrap. With a TLS credential it terminates
// HTTPS (min TLS 1.2) and 301-redirects plain HTTP; without one it serves HTTP.
// The JWT rules are enforced on the serving listener; with no rules the listener
// denies everything (fail closed).
func envoyConfig(rules []shared.IngressJWTRule, tlsCred string) string {
	tls := strings.TrimSpace(tlsCred) != ""
	var listeners []any
	if tls {
		listeners = []any{
			listener("https", httpsPort, hcm(rules), downstreamTLS()),
			listener("http", httpPort, redirectHCM(), nil),
		}
	} else {
		listeners = []any{listener("http", httpPort, hcm(rules), nil)}
	}
	cfg := map[string]any{
		"admin": map[string]any{
			"address": map[string]any{
				"socket_address": map[string]any{"address": "127.0.0.1", "port_value": adminPort},
			},
		},
		"static_resources": map[string]any{
			"listeners": listeners,
			"clusters":  jwksClusters(rules),
		},
	}
	b, _ := yaml.Marshal(cfg)
	return string(b)
}

func listener(name string, port int, hcmTyped map[string]any, transport map[string]any) map[string]any {
	fc := map[string]any{
		"filters": []any{map[string]any{
			"name":         "envoy.filters.network.http_connection_manager",
			"typed_config": hcmTyped,
		}},
	}
	if transport != nil {
		fc["transport_socket"] = transport
	}
	return map[string]any{
		"name":          name,
		"address":       map[string]any{"socket_address": map[string]any{"address": "0.0.0.0", "port_value": port}},
		"filter_chains": []any{fc},
	}
}

// hcm builds the HTTP connection manager. JWT (when rules exist) and the
// security-header Lua run ahead of the router. Since no per-app routing is wired
// yet, an authenticated request with no matching backend gets a 404; with no
// rules the ingress fails closed with a 403.
func hcm(rules []shared.IngressJWTRule) map[string]any {
	var filters []any
	if len(rules) > 0 {
		filters = append(filters, jwtFilter(rules))
	}
	filters = append(filters, luaFilter(), routerFilter())

	var route map[string]any
	if len(rules) > 0 {
		route = directResponse(404, "cortex ingress: authenticated, no route\n")
	} else {
		route = directResponse(403, "cortex ingress: closed (no identity configured)\n")
	}
	return map[string]any{
		"@type":              "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix":        "ingress_http",
		"use_remote_address": true,
		"normalize_path":     true,
		"merge_slashes":      true,
		"http_filters":       filters,
		"route_config": map[string]any{
			"name": "local",
			"virtual_hosts": []any{map[string]any{
				"name":    "default",
				"domains": []any{"*"},
				"routes":  []any{route},
			}},
		},
	}
}

func directResponse(status int, body string) map[string]any {
	return map[string]any{
		"match":           map[string]any{"prefix": "/"},
		"direct_response": map[string]any{"status": status, "body": map[string]any{"inline_string": body}},
	}
}

// redirectHCM serves the plain-HTTP listener when TLS is on: everything 301s to
// HTTPS.
func redirectHCM() map[string]any {
	return map[string]any{
		"@type":        "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix":  "ingress_redirect",
		"http_filters": []any{routerFilter()},
		"route_config": map[string]any{
			"name": "redirect",
			"virtual_hosts": []any{map[string]any{
				"name":    "all",
				"domains": []any{"*"},
				"routes": []any{map[string]any{
					"match":    map[string]any{"prefix": "/"},
					"redirect": map[string]any{"https_redirect": true, "port_redirect": 443},
				}},
			}},
		},
	}
}

// jwtFilter validates Entra JWTs against the pinned issuer rules and requires a
// valid token on every route.
func jwtFilter(rules []shared.IngressJWTRule) map[string]any {
	providers := map[string]any{}
	reqs := make([]any, 0, len(rules))
	var names []string
	for i, r := range rules {
		name := fmt.Sprintf("tenant_%d", i)
		names = append(names, name)
		providers[name] = map[string]any{
			"issuer":    r.Issuer,
			"audiences": toAny(r.Audiences),
			"forward":   true,
			"remote_jwks": map[string]any{
				"http_uri": map[string]any{
					"uri":     r.JWKSURI,
					"cluster": jwksClusterName(r.JWKSURI),
					"timeout": "5s",
				},
				"cache_duration": map[string]any{"seconds": 300},
			},
		}
		reqs = append(reqs, map[string]any{"provider_name": name})
	}
	var requires map[string]any
	if len(reqs) == 1 {
		requires = map[string]any{"provider_name": names[0]}
	} else {
		requires = map[string]any{"requires_any": map[string]any{"requirements": reqs}}
	}
	return map[string]any{
		"name": "envoy.filters.http.jwt_authn",
		"typed_config": map[string]any{
			"@type":     "type.googleapis.com/envoy.extensions.filters.http.jwt_authn.v3.JwtAuthentication",
			"providers": providers,
			"rules": []any{map[string]any{
				"match":    map[string]any{"prefix": "/"},
				"requires": requires,
			}},
		},
	}
}

func luaFilter() map[string]any {
	return map[string]any{
		"name": "envoy.filters.http.lua",
		"typed_config": map[string]any{
			"@type":       "type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua",
			"inline_code": securityHeadersLua,
		},
	}
}

func routerFilter() map[string]any {
	return map[string]any{
		"name": "envoy.filters.http.router",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
		},
	}
}

func downstreamTLS() map[string]any {
	return map[string]any{
		"name": "envoy.transport_sockets.tls",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
			"common_tls_context": map[string]any{
				"tls_params": map[string]any{"tls_minimum_protocol_version": "TLSv1_2"},
				"tls_certificates": []any{map[string]any{
					"certificate_chain": map[string]any{"filename": tlsMountPath + "/tls.crt"},
					"private_key":       map[string]any{"filename": tlsMountPath + "/tls.key"},
				}},
			},
		},
	}
}

// jwksClusters is one upstream per unique JWKS host (Entra's signing-key
// endpoint), reached over TLS with SNI so remote_jwks can fetch keys.
func jwksClusters(rules []shared.IngressJWTRule) []any {
	seen := map[string]bool{}
	out := []any{}
	for _, r := range rules {
		name := jwksClusterName(r.JWKSURI)
		if seen[name] {
			continue
		}
		seen[name] = true
		host := jwksHost(r.JWKSURI)
		out = append(out, map[string]any{
			"name":              name,
			"type":              "LOGICAL_DNS",
			"dns_lookup_family": "V4_PREFERRED",
			"connect_timeout":   "5s",
			"load_assignment": map[string]any{
				"cluster_name": name,
				"endpoints": []any{map[string]any{
					"lb_endpoints": []any{map[string]any{
						"endpoint": map[string]any{
							"address": map[string]any{
								"socket_address": map[string]any{"address": host, "port_value": 443},
							},
						},
					}},
				}},
			},
			"transport_socket": map[string]any{
				"name": "envoy.transport_sockets.tls",
				"typed_config": map[string]any{
					"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
					"sni":   host,
				},
			},
		})
	}
	return out
}

func jwksHost(jwksURI string) string {
	if u, err := url.Parse(jwksURI); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "login.microsoftonline.com"
}

func jwksClusterName(jwksURI string) string {
	return "jwks_" + strings.NewReplacer(".", "_", "-", "_", ":", "_").Replace(jwksHost(jwksURI))
}

func configHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

/* ── Kubernetes resources ──────────────────────────────────────────────────── */

func sysLabels(extra map[string]any) map[string]any {
	m := map[string]any{labelManaged: "true", labelSystem: "true"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func ingressNamespace() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": ingressNS, "labels": sysLabels(nil)},
	}}
}

func ingressServiceAccount() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion":                   "v1",
		"kind":                         "ServiceAccount",
		"metadata":                     map[string]any{"name": ingressName, "namespace": ingressNS, "labels": sysLabels(nil)},
		"automountServiceAccountToken": false,
	}}
}

func ingressConfigMap(cfg string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": ingressCMName, "namespace": ingressNS, "labels": sysLabels(nil)},
		"data":       map[string]any{"envoy.yaml": cfg},
	}}
}

// ingressDeployment runs Envoy locked down: non-root, read-only root filesystem,
// all capabilities dropped, seccomp on. The config-hash annotation rolls the pods
// whenever the rendered Envoy config (JWT rules / TLS) changes.
func ingressDeployment(cfg, tlsCred string) *unstructured.Unstructured {
	tls := strings.TrimSpace(tlsCred) != ""

	ports := []any{map[string]any{"containerPort": int64(httpPort), "name": "http"}}
	volumeMounts := []any{
		map[string]any{"name": "config", "mountPath": "/etc/envoy", "readOnly": true},
		map[string]any{"name": "tmp", "mountPath": "/tmp"},
	}
	volumes := []any{
		map[string]any{"name": "config", "configMap": map[string]any{"name": ingressCMName}},
		map[string]any{"name": "tmp", "emptyDir": map[string]any{}},
	}
	if tls {
		ports = append(ports, map[string]any{"containerPort": int64(httpsPort), "name": "https"})
		volumeMounts = append(volumeMounts, map[string]any{"name": "tls", "mountPath": tlsMountPath, "readOnly": true})
		volumes = append(volumes, map[string]any{"name": "tls", "secret": map[string]any{"secretName": tlsCred}})
	}

	labels := map[string]any{"app": ingressName}
	podLabels := sysLabels(map[string]any{"app": ingressName})

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": ingressName, "namespace": ingressNS, "labels": sysLabels(nil)},
		"spec": map[string]any{
			"replicas": int64(2),
			"selector": map[string]any{"matchLabels": labels},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels":      podLabels,
					"annotations": map[string]any{configHashAnnotation: configHash(cfg + "|" + tlsCred)},
				},
				"spec": map[string]any{
					"serviceAccountName":           ingressName,
					"automountServiceAccountToken": false,
					"securityContext": map[string]any{
						"runAsNonRoot":   true,
						"runAsUser":      int64(101),
						"seccompProfile": map[string]any{"type": "RuntimeDefault"},
					},
					"containers": []any{map[string]any{
						"name":  "envoy",
						"image": envoyImage,
						"args":  []any{"-c", "/etc/envoy/envoy.yaml", "--service-cluster", ingressName, "--log-level", "info"},
						"ports": ports,
						"resources": map[string]any{
							"requests": map[string]any{"cpu": "100m", "memory": "128Mi"},
							"limits":   map[string]any{"cpu": "2", "memory": "512Mi"},
						},
						"securityContext": map[string]any{
							"allowPrivilegeEscalation": false,
							"privileged":               false,
							"readOnlyRootFilesystem":   true,
							"runAsNonRoot":             true,
							"capabilities":             map[string]any{"drop": []any{"ALL"}},
						},
						"volumeMounts": volumeMounts,
						"readinessProbe": map[string]any{
							"tcpSocket":           map[string]any{"port": int64(httpPort)},
							"initialDelaySeconds": int64(2),
							"periodSeconds":       int64(10),
						},
					}},
					"volumes": volumes,
				},
			},
		},
	}}
}

// ingressService exposes Envoy as a public LoadBalancer (the cluster's gateway
// address), preserving the client source IP.
func ingressService(tlsCred string) *unstructured.Unstructured {
	ports := []any{map[string]any{"name": "http", "port": int64(80), "targetPort": int64(httpPort), "protocol": "TCP"}}
	if strings.TrimSpace(tlsCred) != "" {
		ports = append(ports, map[string]any{"name": "https", "port": int64(443), "targetPort": int64(httpsPort), "protocol": "TCP"})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": ingressName, "namespace": ingressNS, "labels": sysLabels(nil)},
		"spec": map[string]any{
			"type":                  "LoadBalancer",
			"externalTrafficPolicy": "Local",
			"selector":              map[string]any{"app": ingressName},
			"ports":                 ports,
		},
	}}
}

// argoTenantProject bounds tenant Helm apps: any source repo, but barred from the
// platform/system namespaces so a tenant app can never touch Argo or the ingress.
func argoTenantProject() *unstructured.Unstructured {
	dests := []any{map[string]any{"server": kubeAPIServer, "namespace": "*"}}
	for _, ns := range protectedNamespaceList {
		dests = append(dests, map[string]any{"server": kubeAPIServer, "namespace": "!" + ns})
	}
	anyResource := []any{map[string]any{"group": "*", "kind": "*"}}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "AppProject",
		"metadata": map[string]any{
			"name":      projectTenants,
			"namespace": argoNamespace,
			"labels":    sysLabels(nil),
		},
		"spec": map[string]any{
			"sourceRepos":                []any{"*"},
			"destinations":               dests,
			"clusterResourceWhitelist":   anyResource,
			"namespaceResourceWhitelist": anyResource,
		},
	}}
}
