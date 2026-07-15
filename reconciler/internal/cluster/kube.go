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

// reconcileApplications stamps a desired Argo Application for each deployment,
// prunes managed Applications no longer desired, and reports each app's status.
func (k *kube) reconcileApplications(ctx context.Context, apps []shared.DesiredApplication) []shared.ApplicationStatus {
	out := make([]shared.ApplicationStatus, 0, len(apps))
	desired := map[string]bool{}
	ri := k.dyn.Resource(appGVR).Namespace(argoNamespace)

	for _, a := range apps {
		name := appName(a.ID)
		desired[name] = true
		k.ensureWorkloadNamespace(ctx, a.Namespace) // make sure the target namespace exists
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

	if list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: labelManaged + "=true," + labelSystem + "!=true"}); err == nil {
		for i := range list.Items {
			n := list.Items[i].GetName()
			if !desired[n] {
				_ = ri.Delete(ctx, n, metav1.DeleteOptions{})
			}
		}
	}
	return out
}

// ingressResult is what reconcileIngress reports back for the heartbeat.
type ingressResult struct {
	ingressInstalled bool
	gatewayIP        string
	issuer           string
}

// ssaOpts is the server-side-apply option every apply uses.
var ssaOpts = metav1.ApplyOptions{FieldManager: fieldManager, Force: true}

// reconcileIngress installs the standalone Envoy ingress: a hardened public
// LoadBalancer whose Envoy config enforces the tenant's Entra JWT (native
// jwt_authn) and hardens response headers. The config is rendered from the
// tenant's issuer rules — with none it fails closed (403). It also stamps the
// restricted tenant Argo project. Reports ingress presence, the public address,
// and the enforced issuer ("" ⇒ closed).
func (k *kube) reconcileIngress(ctx context.Context, o Options, auth *shared.IngressAuth) ingressResult {
	// Bound tenant Helm apps first, so they're restricted the moment they're stamped.
	_, _ = k.dyn.Resource(prjGVR).Namespace(argoNamespace).Apply(ctx, projectTenants, argoTenantProject(), ssaOpts)

	rules := validAuthRules(auth)
	cfg := envoyConfig(rules, o.IngressTLSCredentialName)

	_, _ = k.dyn.Resource(nsGVR).Apply(ctx, ingressNS, ingressNamespace(), ssaOpts)
	_, _ = k.dyn.Resource(saGVR).Namespace(ingressNS).Apply(ctx, ingressName, ingressServiceAccount(), ssaOpts)
	_, _ = k.dyn.Resource(cmGVR).Namespace(ingressNS).Apply(ctx, ingressCMName, ingressConfigMap(cfg), ssaOpts)
	_, _ = k.dyn.Resource(depGVR).Namespace(ingressNS).Apply(ctx, ingressName, ingressDeployment(cfg, o.IngressTLSCredentialName), ssaOpts)
	_, _ = k.dyn.Resource(svcGVR).Namespace(ingressNS).Apply(ctx, ingressName, ingressService(o.IngressTLSCredentialName), ssaOpts)

	res := ingressResult{gatewayIP: k.ingressIP(ctx)}
	if _, err := k.dyn.Resource(depGVR).Namespace(ingressNS).Get(ctx, ingressName, metav1.GetOptions{}); err == nil {
		res.ingressInstalled = true
	}
	if len(rules) > 0 {
		res.issuer = rules[0].Issuer
	}
	return res
}

// ingressIP returns the public address (IP or hostname) of the Envoy ingress
// LoadBalancer Service, or "" until Azure has assigned one.
func (k *kube) ingressIP(ctx context.Context) string {
	svc, err := k.dyn.Resource(svcGVR).Namespace(ingressNS).Get(ctx, ingressName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	ing, found, _ := unstructured.NestedSlice(svc.Object, "status", "loadBalancer", "ingress")
	if !found || len(ing) == 0 {
		return ""
	}
	m, ok := ing[0].(map[string]any)
	if !ok {
		return ""
	}
	if ip, ok := m["ip"].(string); ok && ip != "" {
		return ip
	}
	if host, ok := m["hostname"].(string); ok {
		return host
	}
	return ""
}

func buildApplication(a shared.DesiredApplication, name string) *unstructured.Unstructured {
	source := map[string]any{
		"repoURL":        a.RepoURL,
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
