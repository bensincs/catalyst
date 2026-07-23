package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/inception42/cortex/shared"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/yaml"
)

// kube is a thin wrapper over a dynamic client + discovery for one cluster.
type kube struct {
	dyn   dynamic.Interface
	disco discovery.DiscoveryInterface
}

// argoInstalled reports whether Argo CD's server is present (⇒ CRDs installed).
// A genuine connection/authorization failure is returned as an error so the
// caller reports the cluster unreachable rather than "not installed".
func (k *kube) argoInstalled(ctx context.Context) (bool, error) {
	_, err := k.dyn.Resource(depGVR).Namespace(argoNamespace).Get(ctx, "argocd-server", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (k *kube) ensureNamespace(ctx context.Context, name string) error {
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name},
	}}
	_, err := k.dyn.Resource(nsGVR).Apply(ctx, name, ns, metav1.ApplyOptions{FieldManager: fieldManager, Force: true})
	return err
}

// protectedNamespaceList is the deterministic set of namespaces tenant apps may
// never deploy into — the platform/ingress namespaces and Kubernetes' own system
// namespaces. Kept as an ordered slice so the derived Argo project denies apply
// without churn.
var protectedNamespaceList = []string{
	argoNamespace, ingressNS,
	"kube-system", "kube-public", "kube-node-lease", "default", "gatekeeper-system",
}

var protectedNamespaces = func() map[string]bool {
	m := make(map[string]bool, len(protectedNamespaceList))
	for _, n := range protectedNamespaceList {
		m[n] = true
	}
	return m
}()

// ensureWorkloadNamespace makes sure a tenant workload namespace exists before
// Argo deploys into it. It never touches a protected namespace.
func (k *kube) ensureWorkloadNamespace(ctx context.Context, name string) {
	if name == "" || protectedNamespaces[name] {
		return
	}
	_ = k.ensureNamespace(ctx, name)
}

// applyYAML server-side-applies every document in a multi-doc manifest. Objects
// whose CRD isn't established yet are skipped (the reconcile loop retries), so a
// fresh Argo install converges over a couple of cycles.
func (k *kube) applyYAML(ctx context.Context, data []byte, defaultNS string) error {
	mapper, err := k.restMapper()
	if err != nil {
		return err
	}
	var firstErr error
	for _, doc := range splitDocs(data) {
		m := map[string]any{}
		if err := yaml.Unmarshal(doc, &m); err != nil || len(m) == 0 {
			continue
		}
		obj := &unstructured.Unstructured{Object: m}
		gvk := obj.GroupVersionKind()
		if gvk.Kind == "" {
			continue
		}
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s not yet available", gvk.Kind) // CRD establishing; retried
			}
			continue
		}
		var ri dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ns := obj.GetNamespace()
			if ns == "" {
				ns = defaultNS
				obj.SetNamespace(ns)
			}
			ri = k.dyn.Resource(mapping.Resource).Namespace(ns)
		} else {
			ri = k.dyn.Resource(mapping.Resource)
		}
		if _, err := ri.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{FieldManager: fieldManager, Force: true}); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("apply %s/%s: %w", gvk.Kind, obj.GetName(), err)
			}
		}
	}
	return firstErr
}

func (k *kube) restMapper() (meta.RESTMapper, error) {
	gr, err := restmapper.GetAPIGroupResources(k.disco)
	if err != nil {
		return nil, err
	}
	return restmapper.NewDiscoveryRESTMapper(gr), nil
}

// reconcileApplications stamps a desired Argo Application + an AGIC Ingress for
// each deployment, prunes managed Applications/Ingresses no longer desired, and
// reports each app's status. The Ingress routes the app's host to the Helm
// release's Service (release name : 80) so the Azure Application Gateway serves
// it publicly.
func (k *kube) reconcileApplications(ctx context.Context, apps []shared.DesiredApplication, o Options) []shared.ApplicationStatus {
	out := make([]shared.ApplicationStatus, 0, len(apps))
	desired := map[string]bool{}
	exposed := map[string]bool{}     // app names that publish a gateway Ingress
	ociRepos := map[string]string{} // Argo repo-secret name → OCI registry URL
	ri := k.dyn.Resource(appGVR).Namespace(argoNamespace)

	for _, a := range apps {
		name := appName(a.ID)
		desired[name] = true
		// Any OCI-registry repoURL (no http(s):// scheme) gets an auto-registered
		// Argo Helm repo so the chart pulls over OCI — public ones with no creds.
		if url := ociRegistryURL(a.RepoURL); url != "" {
			ociRepos[ociSecretName(url)] = url
		}
		k.ensureWorkloadNamespace(ctx, a.Namespace) // make sure the target namespace exists
		// Expose the app through the App Gateway only when it declares the Service
		// to route to (charts name it unpredictably); empty ⇒ cluster-internal.
		if svc := strings.TrimSpace(a.ExposeService); svc != "" {
			ing := appIngress(name, a.Namespace, a.ID, appHost(name, o.AppsDomain), svc, a.ExposePort)
			_, _ = k.dyn.Resource(ingGVR).Namespace(a.Namespace).Apply(ctx, name, ing, ssaOpts)
			exposed[name] = true
		}
		st := shared.ApplicationStatus{ID: a.ID, SyncStatus: "pending", HealthStatus: "pending"}
		if _, err := ri.Apply(ctx, name, buildApplication(a, name), metav1.ApplyOptions{FieldManager: fieldManager, Force: true}); err != nil {
			st.SyncStatus, st.HealthStatus = "Unknown", "Unknown"
			out = append(out, st)
			continue
		}
		if cur, err := ri.Get(ctx, name, metav1.GetOptions{}); err == nil {
			if sync, ok, _ := unstructured.NestedString(cur.Object, "status", "sync", "status"); ok && sync != "" {
				st.SyncStatus = sync
			}
			if health, ok, _ := unstructured.NestedString(cur.Object, "status", "health", "status"); ok && health != "" {
				st.HealthStatus = health
			}
		}
		out = append(out, st)
	}

	managed := metav1.ListOptions{LabelSelector: labelManaged + "=true," + labelSystem + "!=true"}
	if list, err := ri.List(ctx, managed); err == nil {
		for i := range list.Items {
			n := list.Items[i].GetName()
			if !desired[n] {
				_ = ri.Delete(ctx, n, metav1.DeleteOptions{})
			}
		}
	}
	// GC Ingresses cluster-wide (namespaced resource, listed across all namespaces)
	// for apps that are gone or no longer expose a Service, so they stop serving.
	if list, err := k.dyn.Resource(ingGVR).List(ctx, managed); err == nil {
		for i := range list.Items {
			n := list.Items[i].GetName()
			if !exposed[n] {
				_ = k.dyn.Resource(ingGVR).Namespace(list.Items[i].GetNamespace()).Delete(ctx, n, metav1.DeleteOptions{})
			}
		}
	}
	k.reconcileHelmOCIRepos(ctx, o, ociRepos)
	return out
}

// ssaOpts is the server-side-apply option every apply uses.
var ssaOpts = metav1.ApplyOptions{FieldManager: fieldManager, Force: true}

// ensureTenantProject stamps the restricted Argo project that bounds tenant Helm
// apps (allowed sources/destinations), so they're constrained the moment they're
// stamped.
func (k *kube) ensureTenantProject(ctx context.Context) {
	_, _ = k.dyn.Resource(prjGVR).Namespace(argoNamespace).Apply(ctx, projectTenants, argoTenantProject(), ssaOpts)
}

// reconcileHelmOCIRepos registers an Argo CD Helm repository (enableOCI) for each
// distinct OCI registry the tenant's apps reference, so their charts pull over
// OCI. Public registries need no credentials; creds are attached only to a
// registry that matches the optionally-configured private one. Auto-registered
// repos no longer referenced by any app are pruned.
func (k *kube) reconcileHelmOCIRepos(ctx context.Context, o Options, repos map[string]string) {
	sec := k.dyn.Resource(secGVR).Namespace(argoNamespace)
	for name, url := range repos {
		user, pass := "", ""
		if reg := strings.TrimSpace(o.HelmOCIRegistry); reg != "" && strings.HasPrefix(url, reg) {
			user, pass = o.HelmOCIUsername, o.HelmOCIPassword
		}
		_, _ = sec.Apply(ctx, name, helmRepoSecret(name, url, user, pass), ssaOpts)
	}
	if list, err := sec.List(ctx, metav1.ListOptions{LabelSelector: labelOCIRepo + "=true"}); err == nil {
		for i := range list.Items {
			n := list.Items[i].GetName()
			if _, ok := repos[n]; !ok {
				_ = sec.Delete(ctx, n, metav1.DeleteOptions{})
			}
		}
	}
}

// appGatewayIP returns the public address (IP or hostname) the Azure Application
// Gateway assigned, read from any managed app Ingress' status, or "" until AGIC
// has programmed the gateway.
func (k *kube) appGatewayIP(ctx context.Context) string {
	list, err := k.dyn.Resource(ingGVR).List(ctx, metav1.ListOptions{LabelSelector: labelManaged + "=true," + labelSystem + "!=true"})
	if err != nil {
		return ""
	}
	for i := range list.Items {
		ing, found, _ := unstructured.NestedSlice(list.Items[i].Object, "status", "loadBalancer", "ingress")
		if !found || len(ing) == 0 {
			continue
		}
		m, ok := ing[0].(map[string]any)
		if !ok {
			continue
		}
		if ip, ok := m["ip"].(string); ok && ip != "" {
			return ip
		}
		if host, ok := m["hostname"].(string); ok && host != "" {
			return host
		}
	}
	return ""
}

func buildApplication(a shared.DesiredApplication, name string) *unstructured.Unstructured {
	// For an OCI registry, use the scheme-stripped URL so it matches the
	// auto-registered Argo repo secret (Argo keys OCI Helm repos scheme-less).
	repoURL := a.RepoURL
	if u := ociRegistryURL(a.RepoURL); u != "" {
		repoURL = u
	}
	source := map[string]any{
		"repoURL":        repoURL,
		"chart":          a.Chart,
		"targetRevision": a.TargetRevision,
	}
	if strings.TrimSpace(a.Values) != "" {
		source["helm"] = map[string]any{"values": a.Values}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      name,
			"namespace": argoNamespace,
			"labels": map[string]any{
				labelManaged: "true",
				labelAppID:   a.ID,
			},
			// Sync-wave orders deploys so a deployment's dependencies converge
			// before it (the control plane derives Wave from the dependency graph).
			"annotations": map[string]any{
				"argocd.argoproj.io/sync-wave": strconv.Itoa(a.Wave),
			},
			// Cascade-delete the app's workloads when the Application is pruned,
			// so a removed deployment actually stops running (no orphans).
			"finalizers": []any{"resources-finalizer.argocd.argoproj.io"},
		},
		"spec": map[string]any{
			"project": projectTenants,
			"source":  source,
			"destination": map[string]any{
				"server":    "https://kubernetes.default.svc",
				"namespace": a.Namespace,
			},
			"syncPolicy": map[string]any{
				"automated":   map[string]any{"prune": true, "selfHeal": true},
				"syncOptions": []any{"CreateNamespace=true"},
			},
		},
	}}
}

// appName maps a control-plane application id to a valid, stable k8s object name
// (RFC 1123: lowercase alphanumeric + hyphens, ≤53 chars for Argo).
func appName(id string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(id) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case b.Len() > 0 && !prevHyphen:
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "app"
	}
	if len(s) > 53 {
		s = strings.Trim(s[:53], "-")
	}
	return s
}

// splitDocs splits a multi-document YAML stream on `---` separators.
func splitDocs(data []byte) [][]byte {
	var out [][]byte
	for _, part := range strings.Split(string(data), "\n---") {
		if strings.TrimSpace(part) != "" {
			out = append(out, []byte(part))
		}
	}
	return out
}
