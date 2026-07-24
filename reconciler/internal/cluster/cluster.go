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
	"bytes"
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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Cluster kinds — how the reconciler reaches the tenant's cluster. Mirrors
// config.ClusterKind*; kept local so this package doesn't import config.
const (
	kindAKS        = "aks"        // Cortex-provisioned AKS via ARM + AKS AAD token
	kindKubeconfig = "kubeconfig" // bring-your-own (Arc) cluster via a supplied kubeconfig
)

const (
	// The well-known AKS AAD server application — the resource a client requests
	// a token for to authenticate to an AAD-integrated cluster's API server.
	aksAADResource  = "6dae42f8-4368-4678-94ff-3960e28e3630"
	armScope        = "https://management.azure.com/.default"
	armAPIVersion   = "2024-09-01"
	vnetAPIVersion  = "2023-11-01" // Microsoft.Network/virtualNetworks (AGC subnet lookup)
	argoNamespace   = "argocd"
	fieldManager    = "cortex-reconciler"
	argoManifestFmt = "https://raw.githubusercontent.com/argoproj/argo-cd/%s/manifests/install.yaml"
	// agcNSGRule opens the AGC subnet's NSG for inbound client traffic to the
	// frontend. The AKS add-on's subnet NSG only has the default rules (ending in
	// DenyAllInBound), which drops client SYNs to the AGC data-path proxies that
	// hold the frontend IP — so the public FQDN times out at TCP connect without it.
	agcNSGRuleName     = "AllowAGCFrontendInbound"
	agcNSGRulePriority = 100
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
	appGVR   = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	prjGVR   = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "appprojects"}
	nsGVR    = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	podGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	nodeGVR  = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	crdGVR   = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	depGVR   = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	secGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	ingGVR   = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"} // legacy AGIC cleanup
	albGVR   = schema.GroupVersionResource{Group: "alb.networking.azure.io", Version: "v1", Resource: "applicationloadbalancer"} // CRD plural is non-standard (no trailing s)
	gwGVR    = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	routeGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
)

// Options is the full address + policy for one tenant's cluster. Grouping them
// keeps the constructor stable as the platform surface grows.
type Options struct {
	// Kind selects how the reconciler reaches the cluster: "aks" (ARM + AKS AAD
	// token, AGC ingress) or "kubeconfig" (a bring-your-own/Arc cluster reached
	// directly from Kubeconfig, no ARM, no AGC).
	Kind           string
	SubscriptionID string
	ResourceGroup  string
	ClusterName    string
	ArgoVersion    string
	// Kubeconfig is the raw (decoded) bring-your-own cluster kubeconfig, used when
	// Kind == "kubeconfig". Must carry embedded CA data + static (token or client
	// certificate) credentials — exec/auth-provider plugins aren't supported.
	Kubeconfig []byte
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
	// Application. For an AKS cluster the reconciler also programs Application
	// Gateway for Containers (a shared Gateway + per-app HTTPRoutes) and reports
	// its FQDN. A bring-your-own (kubeconfig/Arc) cluster has no AGC — the customer
	// owns ingress — so the reconciler stamps apps only and leaves the gateway be.
	k.ensureTenantProject(ctx)
	if c.o.Kind != kindKubeconfig {
		subnet := c.agcSubnetID(ctx, m.nodeResourceGroup)
		c.ensureAGCSubnetNSG(ctx, subnet)
		k.ensureGateway(ctx, subnet)
		appStatuses := k.reconcileApplications(ctx, apps, c.o)
		status.GatewayIP = k.gatewayAddress(ctx)
		status.IngressInstalled = status.GatewayIP != ""
		slog.Info("cluster: gateway reconcile", "nodeRG", m.nodeResourceGroup, "subnetFound", subnet != "", "address", status.GatewayIP)
		if status.GatewayIP == "" {
			k.diagnoseGateway(ctx)
		}
		// Each app's Azure infra is provisioned by the control plane (via Lighthouse)
		// and its outputs are already merged into the Helm values by the time an app
		// is served here — the reconciler just stamps the Argo CD Application.
		return status, appStatuses
	}

	appStatuses := k.reconcileApplications(ctx, apps, c.o)
	status.IngressInstalled = false
	slog.Info("cluster: BYO kubeconfig reconcile", "cluster", c.o.ClusterName, "apps", len(appStatuses))
	return status, appStatuses
}

// --- ARM (cluster metadata + kubeconfig) ------------------------------------

type clusterMeta struct {
	provisioningState string
	k8sVersion        string
	nodeCount         int
	nodeResourceGroup string // MC_ RG where the AGC subnet lives
}

func (c *Client) getCluster(ctx context.Context) (clusterMeta, error) {
	if c.o.Kind == kindKubeconfig {
		return c.getClusterKubeconfig(ctx)
	}
	u := c.armURL("")
	var body struct {
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
			KubernetesVersion string `json:"currentKubernetesVersion"`
			NodeResourceGroup string `json:"nodeResourceGroup"`
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
		nodeResourceGroup: body.Properties.NodeResourceGroup,
	}, nil
}

// agcSubnetID finds the AGC association subnet the AKS add-on created in the node
// resource group (delegated to Microsoft.ServiceNetworking/trafficControllers).
// Returns "" when it isn't present yet (add-on still provisioning).
func (c *Client) agcSubnetID(ctx context.Context, nodeRG string) string {
	if nodeRG == "" {
		return ""
	}
	u := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks?api-version=%s",
		c.o.SubscriptionID, nodeRG, vnetAPIVersion)
	var body struct {
		Value []struct {
			Properties struct {
				Subnets []struct {
					ID         string `json:"id"`
					Properties struct {
						Delegations []struct {
							Properties struct {
								ServiceName string `json:"serviceName"`
							} `json:"properties"`
						} `json:"delegations"`
					} `json:"properties"`
				} `json:"subnets"`
			} `json:"properties"`
		} `json:"value"`
	}
	if err := c.arm(ctx, http.MethodGet, u, &body); err != nil {
		slog.Warn("cluster: list AGC subnet failed", "nodeRG", nodeRG, "err", trunc(err.Error()))
		return ""
	}
	for _, v := range body.Value {
		for _, s := range v.Properties.Subnets {
			for _, d := range s.Properties.Delegations {
				if strings.EqualFold(d.Properties.ServiceName, "Microsoft.ServiceNetworking/trafficControllers") {
					return s.ID
				}
			}
		}
	}
	return ""
}

// ensureAGCSubnetNSG opens the AGC association subnet's NSG so clients can reach
// the Application Gateway for Containers frontend. The AKS add-on gives that
// subnet a dedicated NSG with only the default rules — whose DenyAllInBound drops
// inbound Internet SYNs to the AGC data-path proxies (which hold the frontend IP),
// so the public FQDN times out at TCP connect. Idempotent: skips the write once
// the rule is present, so it never churns ARM.
func (c *Client) ensureAGCSubnetNSG(ctx context.Context, subnetID string) {
	if subnetID == "" {
		return
	}
	var sub struct {
		Properties struct {
			NetworkSecurityGroup struct {
				ID string `json:"id"`
			} `json:"networkSecurityGroup"`
		} `json:"properties"`
	}
	if err := c.arm(ctx, http.MethodGet, "https://management.azure.com"+subnetID+"?api-version="+vnetAPIVersion, &sub); err != nil {
		slog.Warn("cluster: get AGC subnet for NSG failed", "err", trunc(err.Error()))
		return
	}
	nsgID := sub.Properties.NetworkSecurityGroup.ID
	if nsgID == "" {
		return // no NSG on the subnet ⇒ nothing blocking inbound
	}
	ruleURL := fmt.Sprintf("https://management.azure.com%s/securityRules/%s?api-version=%s", nsgID, agcNSGRuleName, vnetAPIVersion)
	var existing struct {
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
		} `json:"properties"`
	}
	if err := c.arm(ctx, http.MethodGet, ruleURL, &existing); err == nil && existing.Properties.ProvisioningState != "" {
		return // already present
	}
	rule := map[string]any{"properties": map[string]any{
		"priority":                 agcNSGRulePriority,
		"direction":                "Inbound",
		"access":                   "Allow",
		"protocol":                 "Tcp",
		"sourceAddressPrefix":      "Internet",
		"sourcePortRange":          "*",
		"destinationAddressPrefix": "*",
		"destinationPortRanges":    []string{"80", "443"},
		"description":              "Allow inbound client traffic to the Application Gateway for Containers frontend.",
	}}
	if err := c.armPut(ctx, ruleURL, rule); err != nil {
		slog.Warn("cluster: open AGC subnet NSG failed", "err", trunc(err.Error()))
		return
	}
	slog.Info("cluster: opened AGC subnet NSG for inbound frontend traffic", "nsg", nsgID)
}

// kubeClient lists the AAD (user) kubeconfig via ARM, then builds a kube client
// that authenticates as this managed identity with an AKS AAD token.
func (c *Client) kubeClient(ctx context.Context) (*kube, error) {
	if c.o.Kind == kindKubeconfig {
		return c.kubeClientKubeconfig()
	}
	// The ARM action is singular: listClusterUserCredential (the built-in AKS
	// Cluster User Role grants exactly that). The plural form is a 404 that a
	// scoped identity sees as a 403, so the cluster looks permanently unreachable.
	u := c.armURL("/listClusterUserCredential")
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

// --- Bring-your-own (kubeconfig / Arc) cluster ------------------------------

// byoRESTConfig builds a verified REST config from the supplied bring-your-own
// kubeconfig. It honors the kubeconfig's OWN embedded credentials (unlike the
// AKS path, which mints an AAD token) and enforces the same TLS-safety floor as
// parseKubeconfig: HTTPS, a pinned CA, no skip-verify. Exec / auth-provider
// plugins are refused (no helper binary ships in the reconciler image), so the
// kubeconfig must carry a static token or client certificate inline — produce
// one with `kubectl config view --flatten --minify`.
func (c *Client) byoRESTConfig() (*rest.Config, error) {
	if len(c.o.Kubeconfig) == 0 {
		return nil, errors.New("bring-your-own cluster: no kubeconfig provided")
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(c.o.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	if !strings.HasPrefix(strings.ToLower(cfg.Host), "https://") {
		return nil, fmt.Errorf("refusing non-HTTPS API server %q", cfg.Host)
	}
	if cfg.Insecure {
		return nil, errors.New("kubeconfig sets insecure-skip-tls-verify — refusing")
	}
	if len(cfg.TLSClientConfig.CAData) == 0 {
		return nil, errors.New("kubeconfig has no inline certificate authority — refusing unverified TLS (use kubectl config view --flatten)")
	}
	if cfg.ExecProvider != nil || cfg.AuthProvider != nil {
		return nil, errors.New("kubeconfig uses an exec/auth-provider plugin — unsupported; use a static token or client-certificate kubeconfig")
	}
	if cfg.BearerToken == "" && len(cfg.TLSClientConfig.CertData) == 0 {
		return nil, errors.New("kubeconfig has no inline credentials — embed a service-account token or client certificate (kubectl config view --flatten)")
	}
	cfg.Timeout = 60 * time.Second
	return cfg, nil
}

// kubeClientKubeconfig builds a kube client for a bring-your-own cluster straight
// from its kubeconfig.
func (c *Client) kubeClientKubeconfig() (*kube, error) {
	cfg, err := c.byoRESTConfig()
	if err != nil {
		return nil, err
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

// getClusterKubeconfig reports a bring-your-own cluster's metadata by talking to
// it directly: reachability + Kubernetes version from the discovery endpoint, and
// node count from the API. There is no ARM provisioning state for a cluster Cortex
// doesn't own, so a reachable cluster is reported "Succeeded".
func (c *Client) getClusterKubeconfig(ctx context.Context) (clusterMeta, error) {
	k, err := c.kubeClientKubeconfig()
	if err != nil {
		return clusterMeta{}, err
	}
	ver, err := k.disco.ServerVersion()
	if err != nil {
		return clusterMeta{}, fmt.Errorf("reach BYO cluster: %w", err)
	}
	nodeCount := 0
	if list, err := k.dyn.Resource(nodeGVR).List(ctx, metav1.ListOptions{}); err == nil {
		nodeCount = len(list.Items)
	}
	return clusterMeta{
		provisioningState: "Succeeded",
		k8sVersion:        ver.GitVersion,
		nodeCount:         nodeCount,
	}, nil
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

// armPut sends a JSON body to ARM (for resource writes like NSG security rules)
// as this managed identity. It discards the response body — callers that need it
// should read the resource back with arm.
func (c *Client) armPut(ctx context.Context, url string, body any) error {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return fmt.Errorf("acquire ARM token: %w", err)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("arm PUT: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
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
