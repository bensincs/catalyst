package cluster

import (
	"fmt"
	"strconv"

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
)

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
			"project": "default",
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
func defaultGateway(tlsSecret string) *unstructured.Unstructured {
	var servers []any
	if tlsSecret != "" {
		servers = []any{
			map[string]any{
				"port":  map[string]any{"number": int64(443), "name": "https", "protocol": "HTTPS"},
				"hosts": []any{"*"},
				"tls": map[string]any{
					"mode":               "SIMPLE",
					"credentialName":     tlsSecret,
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

// securityHeadersFilter adds hardened response headers (HSTS, no-sniff,
// frame-deny, strict referrer + CSP) and strips server-fingerprint headers on
// everything leaving the ingress gateway.
func securityHeadersFilter() *unstructured.Unstructured {
	lua := `function envoy_on_response(handle)
  local h = handle:headers()
  h:replace("strict-transport-security", "max-age=63072000; includeSubDomains; preload")
  h:replace("x-content-type-options", "nosniff")
  h:replace("x-frame-options", "DENY")
  h:replace("referrer-policy", "no-referrer")
  h:replace("content-security-policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
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

// requestAuthentication validates Entra JWTs presented at the ingress gateway
// against the supplied issuer rules. It does NOT by itself reject tokenless
// requests — requireJWTPolicy does that.
func requestAuthentication(auth *shared.IngressAuth) *unstructured.Unstructured {
	rules := make([]any, 0, len(auth.Rules))
	for _, r := range auth.Rules {
		jr := map[string]any{"issuer": r.Issuer, "jwksUri": r.JWKSURI}
		if len(r.Audiences) > 0 {
			jr["audiences"] = toAny(r.Audiences)
		}
		rules = append(rules, jr)
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
			"jwtRules": rules,
		},
	}}
}

// requireJWTPolicy makes a valid token from one of the pinned issuers mandatory:
// requests whose principal (iss/sub) doesn't match any issuer — including
// tokenless requests, which have no principal — are denied at the gateway.
func requireJWTPolicy(auth *shared.IngressAuth) *unstructured.Unstructured {
	principals := make([]any, 0, len(auth.Rules))
	for _, r := range auth.Rules {
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

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
