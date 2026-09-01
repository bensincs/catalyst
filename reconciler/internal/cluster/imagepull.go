package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Pulling the images a chart references.
//
// Argo pulls the chart itself with the tenant's registry token, but the images
// inside it are pulled by the kubelet, which has no credential of its own. A
// chart whose images sit in the platform registry therefore deploys and then
// fails with ImagePullBackOff unless the cluster is given one.
//
// The obvious answer — grant the cluster's kubelet identity AcrPull — does not
// work here: for a delegated tenant the cluster lives in the customer's own
// directory, and granting a customer identity RBAC on a platform resource fails
// with PrincipalNotFound. A registry token is not an identity, which is exactly
// why it works, so the same per-tenant token Argo uses becomes an image pull
// secret in each workload namespace.

// imagePullSecretName is the pull secret written into every workload namespace.
const imagePullSecretName = "cortex-registry"

// dockerConfigJSON builds a docker config for one registry.
func dockerConfigJSON(registry, user, pass string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"auths": map[string]any{
			registry: map[string]any{
				"username": user,
				"password": pass,
				"auth":     base64.StdEncoding.EncodeToString([]byte(user + ":" + pass)),
			},
		},
	})
}

// imagePullSecret renders the dockerconfigjson Secret.
func imagePullSecret(namespace, registry, user, pass string) (*unstructured.Unstructured, error) {
	cfg, err := dockerConfigJSON(registry, user, pass)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "kubernetes.io/dockerconfigjson",
		"metadata": map[string]any{
			"name":      imagePullSecretName,
			"namespace": namespace,
			"labels":    sysLabels(nil),
		},
		"data": map[string]any{
			".dockerconfigjson": base64.StdEncoding.EncodeToString(cfg),
		},
	}}, nil
}

// ensureImagePullSecret puts the registry credential in a workload namespace and
// attaches it to the namespace's default ServiceAccount, so a chart pulls private
// images without its author having to wire anything.
//
// The default ServiceAccount only covers pods that use it. A chart that creates
// its own ServiceAccount has to name the secret itself — which is why the name is
// fixed and documented rather than generated.
func (k *kube) ensureImagePullSecret(ctx context.Context, namespace string, o Options) {
	if o.HelmOCIRegistry == "" || o.HelmOCIUsername == "" || o.HelmOCIPassword == "" {
		return
	}
	if namespace == "" || protectedNamespaces[namespace] {
		return
	}
	sec, err := imagePullSecret(namespace, o.HelmOCIRegistry, o.HelmOCIUsername, o.HelmOCIPassword)
	if err != nil {
		slog.Warn("cluster: build image pull secret failed", "ns", namespace, "err", trunc(err.Error()))
		return
	}
	if _, err := k.dyn.Resource(secGVR).Namespace(namespace).
		Apply(ctx, imagePullSecretName, sec, ssaOpts); err != nil {
		slog.Warn("cluster: apply image pull secret failed", "ns", namespace, "err", trunc(err.Error()))
		return
	}

	// Patch rather than apply: the default ServiceAccount is not ours, and a
	// server-side apply would fight whatever else manages it.
	patch := fmt.Sprintf(`{"imagePullSecrets":[{"name":%q}]}`, imagePullSecretName)
	if _, err := k.dyn.Resource(saGVR).Namespace(namespace).
		Patch(ctx, "default", types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
		slog.Warn("cluster: attach image pull secret failed", "ns", namespace, "err", trunc(err.Error()))
	}
}
