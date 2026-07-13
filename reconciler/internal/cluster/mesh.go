package cluster

import (
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
)

// gatewayValues make the ingress gateway a public LoadBalancer with a stable
// `istio: ingressgateway` label the default Gateway CR selects.
const gatewayValues = "service:\n  type: LoadBalancer\nlabels:\n  istio: ingressgateway\n"

// systemApps are the mesh's Argo Applications, ordered by sync-wave so Istio CRDs
// (base) land before the control plane (istiod) before the gateway.
func systemApps(istioVersion string) []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		meshApp("mesh-istio-base", istioSystemNS, "base", istioVersion, "", -2),
		meshApp("mesh-istiod", istioSystemNS, "istiod", istioVersion, "", -1),
		meshApp("mesh-gateway", istioIngressNS, "gateway", istioVersion, gatewayValues, 0),
	}
}

func meshApp(name, namespace, chart, version, values string, wave int) *unstructured.Unstructured {
	source := map[string]any{"repoURL": istioRepo, "chart": chart}
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

// defaultGateway is an Istio Gateway on the public ingress gateway (wildcard host,
// HTTP :80) that all tenant applications can bind VirtualServices to.
func defaultGateway() *unstructured.Unstructured {
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
			"servers": []any{
				map[string]any{
					"port":  map[string]any{"number": int64(80), "name": "http", "protocol": "HTTP"},
					"hosts": []any{"*"},
				},
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
