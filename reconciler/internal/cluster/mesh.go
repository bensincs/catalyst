package cluster

import (
	"strconv"

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
