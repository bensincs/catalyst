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

	// helmOCISecretName is the Argo CD repository Secret that OCI-enables a Helm
	// registry (e.g. GHCR) so apps pull their chart over OCI.
	helmOCISecretName = "cortex-helm-oci"
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

// helmOCIRepoSecret builds the Argo CD repository Secret that OCI-enables a Helm
// registry, so an Application whose repoURL is that registry pulls its chart over
// OCI. Credentials are included only when set (public registries need none). The
// argocd.argoproj.io/secret-type=repository label is what makes Argo adopt it.
func helmOCIRepoSecret(o Options) *unstructured.Unstructured {
	data := map[string]any{
		"type":      "helm",
		"name":      helmOCISecretName,
		"url":       o.HelmOCIRegistry,
		"enableOCI": "true",
	}
	if strings.TrimSpace(o.HelmOCIUsername) != "" {
		data["username"] = o.HelmOCIUsername
	}
	if strings.TrimSpace(o.HelmOCIPassword) != "" {
		data["password"] = o.HelmOCIPassword
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      helmOCISecretName,
			"namespace": argoNamespace,
			"labels":    sysLabels(map[string]any{"argocd.argoproj.io/secret-type": "repository"}),
		},
		"type":       "Opaque",
		"stringData": data,
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
