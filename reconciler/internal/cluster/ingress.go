package cluster

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Apps are exposed through the AKS-managed Azure Application Gateway. The AGIC
// addon programs the gateway from plain Kubernetes Ingress objects the reconciler
// stamps per app — there's no in-cluster proxy and no edge identity enforcement.
const (
	// ingressNS is retained only to bar tenant apps from a reserved namespace via
	// the Argo project (nothing is deployed here anymore).
	ingressNS = "cortex-ingress"

	// Argo project that bounds tenant Helm apps.
	projectTenants = "cortex-tenants"

	// appGatewayIngressClass routes an Ingress through AGIC ⇒ the Azure
	// Application Gateway.
	appGatewayIngressClass = "azure/application-gateway"
)

const kubeAPIServer = "https://kubernetes.default.svc"

func sysLabels(extra map[string]any) map[string]any {
	m := map[string]any{labelManaged: "true", labelSystem: "true"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// appHost is the per-app public host (<app>.<AppsDomain>) when a domain is
// configured, else "" ⇒ a host-less Ingress served by the gateway's default
// backend.
func appHost(name, domain string) string {
	if strings.TrimSpace(domain) == "" {
		return ""
	}
	return name + "." + strings.TrimSpace(domain)
}

// appIngress exposes one app through the Azure Application Gateway (AGIC). It
// routes the app's host to the Helm release's Service by convention — the release
// name equals the app name, serving on port 80. Labelled managed (not system) so
// the reconciler can list + GC it alongside the app's Argo Application.
func appIngress(name, namespace, appID, host string) *unstructured.Unstructured {
	backend := map[string]any{
		"service": map[string]any{
			"name": name,
			"port": map[string]any{"number": int64(80)},
		},
	}
	rule := map[string]any{
		"http": map[string]any{
			"paths": []any{map[string]any{
				"path":     "/",
				"pathType": "Prefix",
				"backend":  backend,
			}},
		},
	}
	if strings.TrimSpace(host) != "" {
		rule["host"] = host
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				labelManaged: "true",
				labelAppID:   appID,
			},
			"annotations": map[string]any{
				"kubernetes.io/ingress.class": appGatewayIngressClass,
			},
		},
		"spec": map[string]any{
			"rules": []any{rule},
		},
	}}
}

// argoTenantProject bounds tenant Helm apps: any source repo, but barred from the
// platform/system namespaces so a tenant app can never touch Argo or the reserved
// namespaces.
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
