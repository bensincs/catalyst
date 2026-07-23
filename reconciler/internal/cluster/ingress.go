package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Tenant Helm apps are bounded to an Argo project and exposed through Application
// Gateway for Containers (see gateway.go). This file holds the shared cluster
// helpers: labels, the Argo tenant project, per-app host derivation, and OCI Helm
// repo registration.
const (
	// ingressNS is retained only to bar tenant apps from a reserved namespace via
	// the Argo project (nothing is deployed here anymore).
	ingressNS = "cortex-ingress"

	// Argo project that bounds tenant Helm apps.
	projectTenants = "cortex-tenants"
)

const kubeAPIServer = "https://kubernetes.default.svc"

func sysLabels(extra map[string]any) map[string]any {
	m := map[string]any{labelManaged: "true", labelSystem: "true"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// appHost is the per-app public host (<app>.<AppsDomain>) used as the HTTPRoute
// hostname when a domain is configured, else "" ⇒ a host-less route.
func appHost(name, domain string) string {
	if strings.TrimSpace(domain) == "" {
		return ""
	}
	return name + "." + strings.TrimSpace(domain)
}


// ociRegistryURL returns the scheme-stripped OCI registry URL for a repoURL, or
// "" when it's a classic HTTP(S) Helm repo (or empty). OCI Helm registries carry
// no http(s):// scheme — that's how Argo (and we) tell them apart.
func ociRegistryURL(repoURL string) string {
	s := strings.TrimSpace(repoURL)
	if s == "" || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return ""
	}
	return strings.TrimPrefix(s, "oci://")
}

// ociSecretName is a stable, RFC1123-safe Argo repo-secret name for a registry
// URL (its content varies wildly, so hash it).
func ociSecretName(url string) string {
	sum := sha256.Sum256([]byte(url))
	return "cortex-oci-" + hex.EncodeToString(sum[:])[:12]
}

// helmRepoSecret builds an OCI-enabled Argo CD repository Secret for a Helm
// registry, so an Application whose repoURL is that registry pulls its chart over
// OCI. Credentials are included only when set (public registries need none). The
// argocd.argoproj.io/secret-type=repository label is what makes Argo adopt it.
func helmRepoSecret(name, url, user, pass string) *unstructured.Unstructured {
	data := map[string]any{
		"type":      "helm",
		"name":      name,
		"url":       url,
		"enableOCI": "true",
	}
	if strings.TrimSpace(user) != "" {
		data["username"] = user
	}
	if strings.TrimSpace(pass) != "" {
		data["password"] = pass
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": argoNamespace,
			"labels": sysLabels(map[string]any{
				"argocd.argoproj.io/secret-type": "repository",
				labelOCIRepo:                     "true",
			}),
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
