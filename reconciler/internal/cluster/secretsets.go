package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/inception42/cortex/shared"
)

// Materialising a tenant's secret sets as Kubernetes Secrets.
//
// The reconciler is the ONLY component that reads these values, and it is the
// only one that can: the vault lives in the tenant's own subscription, and this
// process runs there with a managed identity holding Key Vault Secrets User on
// it. The control plane wrote the values through the ARM management plane, which
// has no read-back — so the platform put them somewhere it cannot itself look.
//
// A value therefore never appears in the sync payload, in the control plane's
// database, or in the Argo Application. What the author wires into their chart
// is the NAME of the Secret written here, which is not sensitive.

// vaultScope is the Key Vault data plane. Note this is not the ARM scope used
// everywhere else — the management plane cannot read a secret's value, which is
// the entire reason the split exists.
const vaultScope = "https://vault.azure.net/.default"

// vaultSecretVersion pins the API version for the data-plane read.
const vaultAPIVersion = "7.4"

// fetchSecret reads one secret's value from a tenant's vault.
func (c *Client) fetchSecret(ctx context.Context, vaultURI, name string) (string, error) {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{vaultScope}})
	if err != nil {
		return "", fmt.Errorf("acquire vault token: %w", err)
	}
	u := strings.TrimSuffix(vaultURI, "/") + "/secrets/" + url.PathEscape(name) + "?api-version=" + vaultAPIVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		// The body of a Key Vault error does not contain the secret, but it can
		// contain the secret's name and vault, so it is truncated like any other
		// upstream error rather than echoed wholesale.
		return "", fmt.Errorf("vault read %s: %d", name, resp.StatusCode)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("vault read %s: malformed response", name)
	}
	return out.Value, nil
}

// secretSetSecret renders the Kubernetes Secret for one set.
//
// Values go in `data`, base64-encoded, NOT in the friendlier `stringData`.
// stringData is write-only: the API server folds it into data and clears it, so
// server-side apply records ownership of a field that does not persist. Dropping
// a key then removed it from the apply's ownership set while leaving the value
// sitting in data forever — an author who removed a key from a secret store, or
// revoked one, would have been told it was gone while the cluster kept serving
// it. Owning `data` directly is what makes a removed key actually disappear.
func secretSetSecret(name, namespace, setID string, data map[string]string) *unstructured.Unstructured {
	sd := make(map[string]any, len(data))
	for k, v := range data {
		sd[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    sysLabels(map[string]any{labelSecretSet: setID}),
		},
		"data": sd,
	}}
}

// reconcileSecretSets fetches each set's values, writes them into the namespaces
// of the applications that depend on it, and removes the ones that should no
// longer be there.
//
// A set is reported blocked rather than partially written when any key fails to
// read: a Secret missing one key fails later as an obscure crash loop inside
// somebody's chart, whereas refusing to write it surfaces the real reason here.
func (k *kube) reconcileSecretSets(ctx context.Context, c *Client, sets []shared.DesiredSecretSet) []shared.SecretSetStatus {
	out := make([]shared.SecretSetStatus, 0, len(sets))
	for _, s := range sets {
		st := shared.SecretSetStatus{ID: s.ID}
		switch {
		case !s.Complete:
			st.Health = shared.StatusBlocked
			st.Detail = "Some keys still need a value."
			out = append(out, st)
			continue
		case s.VaultURI == "":
			st.Health = shared.StatusBlocked
			st.Detail = "The tenant has no vault yet."
			out = append(out, st)
			continue
		case len(s.Namespaces) == 0:
			// Nothing depends on it, so there is nowhere to put it. Live rather
			// than blocked: the tenant has done everything asked of it.
			st.Health = shared.StatusLive
			st.Detail = "No application depends on this set yet."
			out = append(out, st)
			continue
		}

		data := make(map[string]string, len(s.Keys))
		failed := ""
		for _, key := range s.Keys {
			v, err := c.fetchSecret(ctx, s.VaultURI, shared.VaultSecretName(s.ID, key))
			if err != nil {
				slog.Warn("cluster: secret read failed", "set", s.ID, "key", key, "err", trunc(err.Error()))
				failed = key
				break
			}
			data[key] = v
		}
		if failed != "" {
			st.Health = shared.StatusBlocked
			st.Detail = "Could not read " + failed + " from the vault."
			out = append(out, st)
			continue
		}

		wrote := 0
		for _, ns := range s.Namespaces {
			if ns == "" || protectedNamespaces[ns] {
				continue
			}
			k.ensureWorkloadNamespace(ctx, ns)
			if _, err := k.dyn.Resource(secGVR).Namespace(ns).
				Apply(ctx, s.SecretName, secretSetSecret(s.SecretName, ns, s.ID, data), ssaOpts); err != nil {
				slog.Warn("cluster: apply secret set failed", "set", s.ID, "ns", ns, "err", trunc(err.Error()))
				st.Health = shared.StatusBlocked
				st.Detail = "Could not write the Secret into " + ns + "."
				break
			}
			wrote++
		}
		if st.Health == "" {
			st.Health = shared.StatusLive
			st.Detail = fmt.Sprintf("%d key%s in %d namespace%s.",
				len(data), plural(len(data)), wrote, plural(wrote))
		}
		out = append(out, st)
	}
	k.pruneSecretSets(ctx, sets)
	return out
}

// pruneSecretSets deletes Secrets this reconciler wrote for a set that is no
// longer delivered here — the set was disabled, or the application that pulled
// it into this namespace was removed.
//
// This needs its own pass rather than riding the generic prune: that one selects
// on labelManaged=true,labelSystem!=true, and these carry labelSystem, so they
// were excluded from every existing GC path. Without this, disabling a secret
// store left live credentials sitting in the namespace, readable by anything
// running there — the opposite of what disabling it is for.
func (k *kube) pruneSecretSets(ctx context.Context, sets []shared.DesiredSecretSet) {
	// Namespace+name pairs that should exist after this reconcile.
	keep := map[string]bool{}
	for _, s := range sets {
		if !s.Complete || s.VaultURI == "" {
			continue
		}
		for _, ns := range s.Namespaces {
			keep[ns+"/"+s.SecretName] = true
		}
	}
	// Select on the secret-set label, so a chart's own Secret can never match.
	list, err := k.dyn.Resource(secGVR).List(ctx, metav1.ListOptions{LabelSelector: labelSecretSet})
	if err != nil {
		return
	}
	for i := range list.Items {
		ns, n := list.Items[i].GetNamespace(), list.Items[i].GetName()
		if keep[ns+"/"+n] {
			continue
		}
		if err := k.dyn.Resource(secGVR).Namespace(ns).Delete(ctx, n, metav1.DeleteOptions{}); err != nil {
			slog.Warn("cluster: prune secret set failed", "ns", ns, "name", n, "err", trunc(err.Error()))
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
