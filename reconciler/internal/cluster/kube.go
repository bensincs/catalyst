package cluster

import (
	"context"
	"fmt"
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

// protectedNamespaceList is the deterministic set of namespaces that are never
// mesh-injected or used as tenant workload destinations — the mesh/platform
// namespaces and Kubernetes' own system namespaces. Kept as an ordered slice so
// the derived Argo project denies apply without churn.
var protectedNamespaceList = []string{
	argoNamespace, istioSystemNS, istioIngressNS, observabilityNS,
	"kube-system", "kube-public", "kube-node-lease", "default", "gatekeeper-system",
}

var protectedNamespaces = func() map[string]bool {
	m := make(map[string]bool, len(protectedNamespaceList))
	for _, n := range protectedNamespaceList {
		m[n] = true
	}
	return m
}()

// ensureMeshNamespace enrolls a tenant workload namespace into the mesh
// (istio-injection=enabled) so STRICT mTLS, deny-by-default egress, and
// telemetry actually cover the workloads Argo deploys there. Best-effort: it
// only adds the label (server-side apply), never fighting Argo for the rest of
// the namespace, and never touches a protected namespace.
func (k *kube) ensureMeshNamespace(ctx context.Context, name string) {
	if name == "" || protectedNamespaces[name] {
		return
	}
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":   name,
			"labels": map[string]any{"istio-injection": "enabled"},
		},
	}}
	_, _ = k.dyn.Resource(nsGVR).Apply(ctx, name, ns, ssaOpts)
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
		k.ensureMeshNamespace(ctx, a.Namespace) // enroll the workload's namespace in the mesh
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

// meshResult is what reconcileMesh reports back for the heartbeat.
type meshResult struct {
	meshInstalled bool
	gatewayIP     string
	mtlsStrict    bool
	otelInstalled bool
}

// ssaOpts is the server-side-apply option every mesh CR apply uses.
var ssaOpts = metav1.ApplyOptions{FieldManager: fieldManager, Force: true}

// reconcileMesh installs the mesh as Argo "system" Applications (Istio base +
// istiod, a hardened ingress gateway, the Alloy OTel collector) and applies the
// mesh-wide CRs: STRICT mTLS, the default Gateway, security-header EnvoyFilter,
// and Telemetry. It reports mesh + collector presence and the gateway's public
// address. Best-effort: the CRs need Istio CRDs, which land over a few cycles,
// so errors are tolerated and retried.
func (k *kube) reconcileMesh(ctx context.Context, o Options) meshResult {
	// Restricted Argo projects first, so the mesh + tenant apps are bounded the
	// moment they're stamped.
	for _, p := range argoProjects() {
		_, _ = k.dyn.Resource(prjGVR).Namespace(argoNamespace).Apply(ctx, p.GetName(), p, ssaOpts)
	}
	ri := k.dyn.Resource(appGVR).Namespace(argoNamespace)
	for _, app := range systemApps(o) {
		_, _ = ri.Apply(ctx, app.GetName(), app, ssaOpts)
	}

	// Mesh-wide CRs (depend on Istio CRDs — tolerated + retried until established).
	_, mtlsErr := k.dyn.Resource(paGVR).Namespace(istioSystemNS).Apply(ctx, "default", peerAuthentication(), ssaOpts)
	_, _ = k.dyn.Resource(gwGVR).Namespace(istioIngressNS).Apply(ctx, defaultGWName, defaultGateway(o.IngressTLSCredentialName), ssaOpts)
	_, _ = k.dyn.Resource(efGVR).Namespace(istioIngressNS).Apply(ctx, "cortex-security-headers", securityHeadersFilter(), ssaOpts)
	_, _ = k.dyn.Resource(telGVR).Namespace(istioSystemNS).Apply(ctx, "default", meshTelemetry(), ssaOpts)

	res := meshResult{gatewayIP: k.ingressIP(ctx)}
	if _, err := k.dyn.Resource(depGVR).Namespace(istioSystemNS).Get(ctx, "istiod", metav1.GetOptions{}); err == nil {
		res.meshInstalled = true
	}
	// STRICT mTLS is "active" only when the policy applied and the control plane
	// that enforces it is present.
	res.mtlsStrict = mtlsErr == nil && res.meshInstalled
	if _, err := k.dyn.Resource(depGVR).Namespace(observabilityNS).Get(ctx, alloyServiceName, metav1.GetOptions{}); err == nil {
		res.otelInstalled = true
	}
	return res
}

// reconcileIngressAuth pins the ingress gateway to accept only the tenant's own
// Entra tokens (RequestAuthentication) and requires one on every request
// (AuthorizationPolicy). With no identity configured it fails the gateway CLOSED
// (deny-all) rather than serving open. It returns the enforced issuer, or "" when
// none is configured. Best-effort: the CRs need Istio's security CRDs, which land
// with istiod over a few cycles, so errors are tolerated and retried.
func (k *kube) reconcileIngressAuth(ctx context.Context, auth *shared.IngressAuth) string {
	ap := k.dyn.Resource(apGVR).Namespace(istioIngressNS)
	ra := k.dyn.Resource(raGVR).Namespace(istioIngressNS)
	rules := validAuthRules(auth)
	if len(rules) == 0 {
		// No usable identity ⇒ fail closed: deny all ingress and drop any stale
		// JWT rule.
		_, _ = ap.Apply(ctx, authPolicyName, denyAllPolicy(), ssaOpts)
		_ = ra.Delete(ctx, requestAuthName, metav1.DeleteOptions{})
		return ""
	}
	_, _ = ra.Apply(ctx, requestAuthName, requestAuthentication(rules), ssaOpts)
	_, _ = ap.Apply(ctx, authPolicyName, requireJWTPolicy(rules), ssaOpts)
	return rules[0].Issuer
}

// ingressIP returns the public address (IP or hostname) of the ingress gateway's
// LoadBalancer Service, or "" until Azure has assigned one.
func (k *kube) ingressIP(ctx context.Context) string {
	list, err := k.dyn.Resource(svcGVR).Namespace(istioIngressNS).List(ctx, metav1.ListOptions{LabelSelector: "istio=" + ingressGWLabel})
	if err != nil || len(list.Items) == 0 {
		return ""
	}
	ing, found, _ := unstructured.NestedSlice(list.Items[0].Object, "status", "loadBalancer", "ingress")
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
