package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/inception42/cortex/shared"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The Istio service mesh + a default public ingress gateway are installed as Argo
// CD "system" Applications (so Argo owns their lifecycle + drift), plus a default
// Gateway CR every tenant application can route VirtualServices through.
const (
	istioRepo      = "https://istio-release.storage.googleapis.com/charts"
	istioSystemNS  = "istio-system"
	istioIngressNS = "istio-ingress"
	ingressGWLabel = "ingressgateway" // istio: ingressgateway
	defaultGWName  = "cortex-gateway"

	// otelProviderName is the mesh extensionProvider (declared in istiod's
	// meshConfig) that points at the in-cluster Alloy collector, so Istio ships
	// traces + access logs for every meshed workload to it.
	otelProviderName = "cortex-otel"

	// Argo AppProjects that bound what Cortex deploys: the mesh's own charts vs
	// tenant Helm apps. Both cap blast radius (source repos + destinations).
	projectSystem  = "cortex-system"
	projectTenants = "cortex-tenants"
)

const kubeAPIServer = "https://kubernetes.default.svc"

// argoProjects are the two restricted Argo AppProjects. cortex-system may only
// source the mesh chart repos (so a mis-created "system" app can't pull an
// arbitrary chart) and may manage cluster-scoped resources; cortex-tenants may
// source any repo but is barred from deploying into the platform/system
// namespaces, so a tenant app can never tamper with the mesh, gateway, Argo, or
// the collector.
func argoProjects() []*unstructured.Unstructured {
	dests := []any{map[string]any{"server": kubeAPIServer, "namespace": "*"}}
	for _, ns := range protectedNamespaceList {
		dests = append(dests, map[string]any{"server": kubeAPIServer, "namespace": "!" + ns})
	}
	anyResource := []any{map[string]any{"group": "*", "kind": "*"}}
	return []*unstructured.Unstructured{
		appProject(projectSystem, []any{istioRepo, alloyRepo},
			[]any{map[string]any{"server": kubeAPIServer, "namespace": "*"}}, anyResource, anyResource),
		appProject(projectTenants, []any{"*"}, dests, anyResource, anyResource),
	}
}

func appProject(name string, sourceRepos, destinations, clusterWhitelist, nsWhitelist []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "AppProject",
		"metadata": map[string]any{
			"name":      name,
			"namespace": argoNamespace,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"sourceRepos":                sourceRepos,
			"destinations":               destinations,
			"clusterResourceWhitelist":   clusterWhitelist,
			"namespaceResourceWhitelist": nsWhitelist,
		},
	}}
}

// systemApps are the mesh's Argo Applications, ordered by sync-wave so Istio CRDs
// (base) land before the control plane (istiod) before the gateway + collector.
func systemApps(o Options) []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		meshApp("mesh-istio-base", istioSystemNS, istioRepo, "base", o.IstioVersion, "", -3),
		meshApp("mesh-istiod", istioSystemNS, istioRepo, "istiod", o.IstioVersion, istiodValues(o), -2),
		meshApp("mesh-alloy", observabilityNS, alloyRepo, "alloy", o.AlloyChartVersion, alloyValues(o.OTelExporterEndpoint), -1),
		meshApp("mesh-gateway", istioIngressNS, istioRepo, "gateway", o.IstioVersion, gatewayValues(), 0),
	}
}

func meshApp(name, namespace, repo, chart, version, values string, wave int) *unstructured.Unstructured {
	source := map[string]any{"repoURL": repo, "chart": chart}
	if version != "" {
		source["targetRevision"] = version
	}
	if values != "" {
		source["helm"] = map[string]any{"values": values}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      name,
			"namespace": argoNamespace,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
			"annotations": map[string]any{
				"argocd.argoproj.io/sync-wave": strconv.Itoa(wave),
			},
		},
		"spec": map[string]any{
			"project": projectSystem,
			"source":  source,
			"destination": map[string]any{
				"server":    "https://kubernetes.default.svc",
				"namespace": namespace,
			},
			"syncPolicy": map[string]any{
				"automated":   map[string]any{"prune": true, "selfHeal": true},
				"syncOptions": []any{"CreateNamespace=true", "ServerSideApply=true"},
			},
		},
	}}
}

// istiodValues hardens the mesh: STRICT mTLS is auto-enabled per workload, all
// proxies emit access logs, egress is deny-by-default (REGISTRY_ONLY), apps hold
// startup until their proxy is ready (no unencrypted early traffic), and an OTel
// extensionProvider wires mesh traces/logs to the Alloy collector.
func istiodValues(o Options) string {
	return fmt.Sprintf(`meshConfig:
  accessLogFile: /dev/stdout
  enableTracing: true
  pathNormalization:
    normalization: DECODE_AND_MERGE_SLASHES
  outboundTrafficPolicy:
    mode: %s
  defaultConfig:
    holdApplicationUntilProxyStarts: true
  extensionProviders:
  - name: %s
    opentelemetry:
      service: %s.%s.svc.cluster.local
      port: %d
`, o.OutboundTrafficPolicy, otelProviderName, alloyServiceName, observabilityNS, otlpGRPCPort)
}

// gatewayValues run the ingress gateway as a public LoadBalancer with a stable
// `istio: ingressgateway` label (selected by the Gateway + auth policies), plus a
// locked-down pod: non-root, no privilege escalation, read-only root filesystem,
// all Linux capabilities dropped, seccomp on, HA with resource bounds.
func gatewayValues() string {
	return `service:
  type: LoadBalancer
  externalTrafficPolicy: Local
labels:
  istio: ingressgateway
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 5
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: "2"
    memory: 1024Mi
securityContext:
  runAsNonRoot: true
  runAsUser: 1337
  runAsGroup: 1337
  fsGroup: 1337
  seccompProfile:
    type: RuntimeDefault
containerSecurityContext:
  allowPrivilegeEscalation: false
  privileged: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop:
    - ALL
`
}

// defaultGateway is an Istio Gateway on the public ingress gateway that all
// tenant applications bind VirtualServices to. With a TLS cert secret it
// terminates HTTPS (min TLS 1.2) and 301-redirects plain HTTP to it; without
// one it serves HTTP :80 (auth is still enforced by the JWT policy).
func defaultGateway(credentialName string) *unstructured.Unstructured {
	var servers []any
	if credentialName != "" {
		servers = []any{
			map[string]any{
				"port":  map[string]any{"number": int64(443), "name": "https", "protocol": "HTTPS"},
				"hosts": []any{"*"},
				"tls": map[string]any{
					"mode":               "SIMPLE",
					"credentialName":     credentialName,
					"minProtocolVersion": "TLSV1_2",
				},
			},
			map[string]any{
				"port":  map[string]any{"number": int64(80), "name": "http", "protocol": "HTTP"},
				"hosts": []any{"*"},
				"tls":   map[string]any{"httpsRedirect": true},
			},
		}
	} else {
		servers = []any{
			map[string]any{
				"port":  map[string]any{"number": int64(80), "name": "http", "protocol": "HTTP"},
				"hosts": []any{"*"},
			},
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":      defaultGWName,
			"namespace": istioIngressNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"selector": map[string]any{"istio": ingressGWLabel},
			"servers":  servers,
		},
	}}
}

// peerAuthentication enforces mesh-wide STRICT mTLS: named "default" in the root
// namespace, it requires mutual TLS between every meshed workload, so plaintext
// service-to-service traffic is refused.
func peerAuthentication() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1",
		"kind":       "PeerAuthentication",
		"metadata": map[string]any{
			"name":      "default",
			"namespace": istioSystemNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{"mtls": map[string]any{"mode": "STRICT"}},
	}}
}

// securityHeadersFilter adds hardened response headers on everything leaving the
// ingress gateway. Transport-security headers are always enforced; content
// headers are only defaulted when the app didn't set its own (so we harden bare
// apps without clobbering ones that ship a deliberate policy), and no blanket CSP
// is imposed (that would break most real apps). Server-fingerprint headers are
// stripped.
func securityHeadersFilter() *unstructured.Unstructured {
	lua := `function envoy_on_response(handle)
  local h = handle:headers()
  -- Always enforce transport hardening (independent of app content).
  h:replace("strict-transport-security", "max-age=63072000; includeSubDomains; preload")
  h:replace("x-content-type-options", "nosniff")
  -- Safe defaults only when the app has not set its own.
  if h:get("x-frame-options") == nil and h:get("content-security-policy") == nil then
    h:replace("x-frame-options", "SAMEORIGIN")
  end
  if h:get("referrer-policy") == nil then
    h:replace("referrer-policy", "strict-origin-when-cross-origin")
  end
  -- Don't advertise the server implementation.
  h:remove("server")
  h:remove("x-powered-by")
end`
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.istio.io/v1alpha3",
		"kind":       "EnvoyFilter",
		"metadata": map[string]any{
			"name":      "cortex-security-headers",
			"namespace": istioIngressNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"workloadSelector": map[string]any{"labels": map[string]any{"istio": ingressGWLabel}},
			"configPatches": []any{
				map[string]any{
					"applyTo": "HTTP_FILTER",
					"match": map[string]any{
						"context": "GATEWAY",
						"listener": map[string]any{
							"filterChain": map[string]any{
								"filter": map[string]any{
									"name":      "envoy.filters.network.http_connection_manager",
									"subFilter": map[string]any{"name": "envoy.filters.http.router"},
								},
							},
						},
					},
					"patch": map[string]any{
						"operation": "INSERT_BEFORE",
						"value": map[string]any{
							"name": "envoy.filters.http.lua",
							"typed_config": map[string]any{
								"@type":      "type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua",
								"inlineCode": lua,
							},
						},
					},
				},
			},
		},
	}}
}

// meshTelemetry ships traces + access logs for every meshed workload to the OTel
// collector via the extensionProvider declared in istiod's meshConfig.
func meshTelemetry() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "telemetry.istio.io/v1",
		"kind":       "Telemetry",
		"metadata": map[string]any{
			"name":      "default",
			"namespace": istioSystemNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"tracing": []any{
				map[string]any{
					"providers":                []any{map[string]any{"name": otelProviderName}},
					"randomSamplingPercentage": int64(10),
				},
			},
			"accessLogging": []any{
				map[string]any{"providers": []any{map[string]any{"name": otelProviderName}}},
			},
		},
	}}
}

// Names of the ingress auth CRs (both target the ingress gateway workload).
const (
	requestAuthName = "cortex-jwt"
	authPolicyName  = "cortex-require-jwt"
)

// validAuthRules keeps only fully-formed JWT rules: a rule missing an issuer,
// JWKS URI, or audience would weaken the gateway (accept any audience, or match
// an empty principal), so we drop it rather than apply it.
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

// requestAuthentication validates Entra JWTs presented at the ingress gateway
// against the supplied issuer rules. It does NOT by itself reject tokenless
// requests — requireJWTPolicy does that.
func requestAuthentication(rules []shared.IngressJWTRule) *unstructured.Unstructured {
	jwtRules := make([]any, 0, len(rules))
	for _, r := range rules {
		jwtRules = append(jwtRules, map[string]any{
			"issuer":    r.Issuer,
			"jwksUri":   r.JWKSURI,
			"audiences": toAny(r.Audiences),
		})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1",
		"kind":       "RequestAuthentication",
		"metadata": map[string]any{
			"name":      requestAuthName,
			"namespace": istioIngressNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"istio": ingressGWLabel}},
			"jwtRules": jwtRules,
		},
	}}
}

// requireJWTPolicy makes a valid token from one of the pinned issuers mandatory:
// requests whose principal (iss/sub) doesn't match any issuer — including
// tokenless requests, which have no principal — are denied at the gateway.
func requireJWTPolicy(rules []shared.IngressJWTRule) *unstructured.Unstructured {
	principals := make([]any, 0, len(rules))
	for _, r := range rules {
		principals = append(principals, r.Issuer+"/*")
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1",
		"kind":       "AuthorizationPolicy",
		"metadata": map[string]any{
			"name":      authPolicyName,
			"namespace": istioIngressNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"istio": ingressGWLabel}},
			"action":   "ALLOW",
			"rules": []any{
				map[string]any{
					"from": []any{
						map[string]any{"source": map[string]any{"requestPrincipals": principals}},
					},
				},
			},
		},
	}}
}

// denyAllPolicy fails the gateway closed: it selects the ingress gateway with an
// ALLOW action and no rules, which admits nothing — so with no identity
// configured the gateway rejects all ingress instead of serving open.
func denyAllPolicy() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1",
		"kind":       "AuthorizationPolicy",
		"metadata": map[string]any{
			"name":      authPolicyName,
			"namespace": istioIngressNS,
			"labels":    map[string]any{labelManaged: "true", labelSystem: "true"},
		},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"istio": ingressGWLabel}},
			"action":   "ALLOW",
			// No rules ⇒ nothing matches ⇒ all ingress denied (fail closed).
		},
	}}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
