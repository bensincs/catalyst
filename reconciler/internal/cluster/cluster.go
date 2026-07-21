// Package cluster bootstraps Argo CD into the tenant's AKS cluster and stamps the
// tenant's Helm deployments as Argo CD Application CRs.
//
// The reconciler's own managed identity authenticates to both ARM (to read the
// cluster and list its kubeconfig) and the cluster's AAD-integrated API server
// (authorized by the "Azure Kubernetes Service RBAC Cluster Admin" role the
// managed-app Bicep grants it). There is no static admin kubeconfig and no
// shared secret. The reconcile is idempotent and reports honest status: if it
// can't reach the cluster it says so rather than inventing health.
package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/inception42/cortex/shared"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// The well-known AKS AAD server application — the resource a client requests
	// a token for to authenticate to an AAD-integrated cluster's API server.
	aksAADResource  = "6dae42f8-4368-4678-94ff-3960e28e3630"
	armScope        = "https://management.azure.com/.default"
	armAPIVersion   = "2024-09-01"
	argoNamespace   = "argocd"
	fieldManager    = "cortex-reconciler"
	argoManifestFmt = "https://raw.githubusercontent.com/argoproj/argo-cd/%s/manifests/install.yaml"
)

// Labels Cortex stamps on every Argo Application it manages, so it only ever
// mutates or prunes Applications it owns. System resources (the ingress) also
// carry labelSystem so the tenant-app prune never removes them.
const (
	labelManaged = "cortex.io/managed" // "true"
	labelSystem  = "cortex.io/system"  // "true" for the ingress/system resources
	labelAppID   = "cortex.io/app-id"  // control-plane application id
	labelOCIRepo = "cortex.io/oci-repo" // "true" for auto-registered Argo OCI Helm repos
)

var (
	appGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	prjGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "appprojects"}
	nsGVR  = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	depGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	ingGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	secGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
)

// Options is the full address + policy for one tenant's cluster. Grouping them
// keeps the constructor stable as the platform surface grows.
type Options struct {
	SubscriptionID string
	ResourceGroup  string
	ClusterName    string
	ArgoVersion    string
	// AppsDomain is the DNS suffix for per-app hosts (<app>.<AppsDomain>). Empty
	// ⇒ host-less Ingress (App Gateway default backend).
	AppsDomain string
	// HelmOCIRegistry, when set, registers an OCI-enabled Argo Helm repo so apps
	// with this RepoURL pull their chart over OCI. User/Pass are optional (private).
	HelmOCIRegistry string
	HelmOCIUsername string
	HelmOCIPassword string
}

// Client drives one tenant's cluster (one reconciler → one cluster).
type Client struct {
	cred azcore.TokenCredential
	http *http.Client
	o    Options
}

func New(cred azcore.TokenCredential, o Options) *Client {
	return &Client{
		cred: cred,
		http: &http.Client{
			Timeout: 60 * time.Second,
			// Pin a TLS 1.2 floor for all outbound calls (ARM, Argo manifest),
			// independent of the Go default.
			Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		},
		o: o,
	}
}

// Reconcile ensures Argo CD is installed and the desired Helm deployments are
// stamped as Argo Applications, then returns cluster + per-app status. Apps are
// exposed through the AKS-managed Azure Application Gateway (AGIC) — the edge no
// longer enforces identity, so the auth policy is accepted but ignored.
func (c *Client) Reconcile(ctx context.Context, apps []shared.DesiredApplication, _ *shared.IngressAuth) (shared.ClusterStatus, []shared.ApplicationStatus) {
	status := shared.ClusterStatus{Name: c.o.ClusterName, Phase: shared.ClusterProvisioning}

	m, err := c.getCluster(ctx)
	if err != nil {
		status.Phase = shared.ClusterUnreachable
		status.Detail = trunc(err.Error())
		return status, pending(apps)
	}
	status.KubernetesVer = m.k8sVersion
	status.NodeCount = m.nodeCount
	if !strings.EqualFold(m.provisioningState, "Succeeded") {
		status.Detail = "cluster provisioning: " + m.provisioningState
		return status, pending(apps)
	}

	k, err := c.kubeClient(ctx)
	if err != nil {
		status.Phase = shared.ClusterUnreachable
		status.Detail = trunc(err.Error())
		return status, pending(apps)
	}

	installed, err := k.argoInstalled(ctx)
	if err != nil {
		status.Phase = shared.ClusterUnreachable
		status.Detail = trunc(err.Error())
		return status, pending(apps)
	}
	if !installed {
		if err := c.installArgo(ctx, k); err != nil {
			status.Detail = "installing Argo CD: " + trunc(err.Error())
			return status, pending(apps)
		}
		slog.Info("cluster: applied Argo CD install manifest", "version", c.o.ArgoVersion)
		// CRDs need a moment to establish; converge Applications next cycle.
		status.ArgoInstalled = true
		status.Detail = "Argo CD installing"
		return status, pending(apps)
	}
	status.ArgoInstalled = true
	status.Phase = shared.ClusterReady

	// Bound tenant apps to their Argo project, then stamp each app's Argo
	// Application + a plain Ingress. AGIC programs the Azure Application Gateway
	// from those Ingresses; report the gateway address it assigns.
	k.ensureTenantProject(ctx)
	appStatuses := k.reconcileApplications(ctx, apps, c.o)
	status.GatewayIP = k.appGatewayIP(ctx)
	status.IngressInstalled = status.GatewayIP != ""

	// Each app's Azure infra is provisioned by the control plane (via Lighthouse)
	// and its outputs are already merged into the Helm values by the time an app
	// is served here — the reconciler just stamps the Argo CD Application.
	return status, appStatuses
}

// --- ARM (cluster metadata + kubeconfig) ------------------------------------

type clusterMeta struct {
	provisioningState string
	k8sVersion        string
	nodeCount         int
}

func (c *Client) getCluster(ctx context.Context) (clusterMeta, error) {
	u := c.armURL("")
	var body struct {
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
			KubernetesVersion string `json:"currentKubernetesVersion"`
			AgentPoolProfiles []struct {
				Count int `json:"count"`
			} `json:"agentPoolProfiles"`
		} `json:"properties"`
	}
	if err := c.arm(ctx, http.MethodGet, u, &body); err != nil {
		return clusterMeta{}, err
	}
	n := 0
	for _, p := range body.Properties.AgentPoolProfiles {
		n += p.Count
	}
	return clusterMeta{
		provisioningState: body.Properties.ProvisioningState,
		k8sVersion:        body.Properties.KubernetesVersion,
		nodeCount:         n,
	}, nil
}

// kubeClient lists the AAD (user) kubeconfig via ARM, then builds a kube client
// that authenticates as this managed identity with an AKS AAD token.
func (c *Client) kubeClient(ctx context.Context) (*kube, error) {
	u := c.armURL("/listClusterUserCredentials")
	var resp struct {
		Kubeconfigs []struct {
			Value []byte `json:"value"` // base64 → decoded YAML by encoding/json
		} `json:"kubeconfigs"`
	}
	if err := c.arm(ctx, http.MethodPost, u, &resp); err != nil {
		return nil, err
	}
	if len(resp.Kubeconfigs) == 0 || len(resp.Kubeconfigs[0].Value) == 0 {
		return nil, errors.New("no kubeconfig returned")
	}
	server, ca, err := parseKubeconfig(resp.Kubeconfigs[0].Value)
	if err != nil {
		return nil, err
	}
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{aksAADResource + "/.default"}})
	if err != nil {
		return nil, fmt.Errorf("acquire AKS token: %w", err)
	}
	cfg := &rest.Config{
		Host:            server,
		BearerToken:     tok.Token,
		TLSClientConfig: rest.TLSClientConfig{CAData: ca},
		Timeout:         60 * time.Second,
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &kube{dyn: dyn, disco: disco}, nil
}

func (c *Client) armURL(suffix string) string {
	return fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s%s?api-version=%s",
		c.o.SubscriptionID, c.o.ResourceGroup, c.o.ClusterName, suffix, armAPIVersion)
}

func (c *Client) arm(ctx context.Context, method, url string, out any) error {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return fmt.Errorf("acquire ARM token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("arm %s: %d %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return decodeJSON(resp.Body, out)
}

// --- Argo CD bootstrap ------------------------------------------------------

func (c *Client) installArgo(ctx context.Context, k *kube) error {
	if err := k.ensureNamespace(ctx, argoNamespace); err != nil {
		return err
	}
	manifest, err := c.fetchArgoManifest(ctx)
	if err != nil {
		return err
	}
	return k.applyYAML(ctx, manifest, argoNamespace)
}

func (c *Client) fetchArgoManifest(ctx context.Context) ([]byte, error) {
	url := fmt.Sprintf(argoManifestFmt, c.o.ArgoVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch argo manifest %s: %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// --- helpers ----------------------------------------------------------------

func pending(apps []shared.DesiredApplication) []shared.ApplicationStatus {
	out := make([]shared.ApplicationStatus, 0, len(apps))
	for _, a := range apps {
		out = append(out, shared.ApplicationStatus{ID: a.ID, SyncStatus: "pending", HealthStatus: "pending"})
	}
	return out
}

func parseKubeconfig(data []byte) (server string, ca []byte, err error) {
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return "", nil, err
	}
	for _, cl := range cfg.Clusters {
		// Never talk to an API server without pinned TLS verification: require an
		// HTTPS endpoint, a CA bundle, and no skip-verify, so a tampered or
		// misconfigured kubeconfig can't downgrade us to an unverified connection.
		if !strings.HasPrefix(strings.ToLower(cl.Server), "https://") {
			return "", nil, fmt.Errorf("refusing non-HTTPS API server %q", cl.Server)
		}
		if len(cl.CertificateAuthorityData) == 0 {
			return "", nil, errors.New("kubeconfig has no certificate authority — refusing unverified TLS")
		}
		if cl.InsecureSkipTLSVerify {
			return "", nil, errors.New("kubeconfig sets insecure-skip-tls-verify — refusing")
		}
		return cl.Server, cl.CertificateAuthorityData, nil
	}
	return "", nil, errors.New("no cluster in kubeconfig")
}

func trunc(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func decodeJSON(r io.Reader, out any) error {
	if out == nil {
		return nil
	}
	return json.NewDecoder(r).Decode(out)
}
