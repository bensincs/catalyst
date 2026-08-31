package cluster

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Apps are exposed through Application Gateway for Containers (AGC) via the AKS
// add-on: the ALB controller (installed by the add-on) programs AGC from Gateway
// API objects. The reconciler stamps one ApplicationLoadBalancer (→ the AGC
// resource, associated to the add-on's aks-appgateway subnet) + one shared
// Gateway, then an HTTPRoute per app that routes to the app's Service. Unlike the
// old AGIC ingress, AGC routes via the Service, so it works with CNI Overlay.
const (
	// gatewayNS holds the ApplicationLoadBalancer + Gateway (bounded from tenants).
	gatewayNS = "cortex-gateway"
	// albName is the ApplicationLoadBalancer (AGC) resource; gatewayName the Gateway.
	albName     = "cortex-alb"
	gatewayName = "cortex-gateway"
	// gatewayClass is the AKS-managed GatewayClass the ALB controller owns.
	gatewayClass = "azure-alb-external"
)

// applicationLoadBalancer provisions the AGC resource, associating it to the
// add-on-created subnet (delegated to Microsoft.ServiceNetworking/trafficControllers).
func applicationLoadBalancer(subnetID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "alb.networking.azure.io/v1",
		"kind":       "ApplicationLoadBalancer",
		"metadata":   map[string]any{"name": albName, "namespace": gatewayNS, "labels": sysLabels(nil)},
		"spec":       map[string]any{"associations": []any{subnetID}},
	}}
}

// tlsSecretName holds the wildcard certificate for the tenant's domain. It lives
// in the gateway namespace because that is where the Gateway resolves
// certificateRefs from without a ReferenceGrant.
const tlsSecretName = "cortex-wildcard-tls"

// gateway is the single external Gateway all app HTTPRoutes attach to. The
// annotations bind it to the ALB-managed AGC resource; from:All lets HTTPRoutes in
// the tenant app namespaces attach.
//
// An HTTPS listener is added only once a wildcard certificate exists. AGC allows
// only ports 80 and 443, and a listener referencing a missing Secret is rejected
// outright — so the gateway serves plain HTTP until the control plane has
// obtained a certificate, rather than failing to program at all.
func gateway(domain string, tls bool) *unstructured.Unstructured {
	listeners := []any{map[string]any{
		"name":     "http",
		"port":     int64(80),
		"protocol": "HTTP",
		"allowedRoutes": map[string]any{
			"namespaces": map[string]any{"from": "All"},
		},
	}}
	if tls && strings.TrimSpace(domain) != "" {
		listeners = append(listeners, map[string]any{
			"name":     "https",
			"port":     int64(443),
			"protocol": "HTTPS",
			// The wildcard covers every app host under the tenant's domain, so one
			// listener serves all of them.
			"hostname": "*." + strings.TrimSpace(domain),
			"tls": map[string]any{
				"mode": "Terminate",
				"certificateRefs": []any{map[string]any{
					"kind":      "Secret",
					"name":      tlsSecretName,
					"namespace": gatewayNS,
				}},
			},
			"allowedRoutes": map[string]any{
				"namespaces": map[string]any{"from": "All"},
			},
		})
	}
	return gatewayWithListeners(listeners)
}

func gatewayWithListeners(listeners []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":      gatewayName,
			"namespace": gatewayNS,
			"labels":    sysLabels(nil),
			"annotations": map[string]any{
				"alb.networking.azure.io/alb-namespace": gatewayNS,
				"alb.networking.azure.io/alb-name":      albName,
			},
		},
		"spec": map[string]any{
			"gatewayClassName": gatewayClass,
			"listeners":        listeners,
		},
	}}
}

// wildcardTLSSecret carries the certificate the control plane obtained for the
// tenant's domain. The reconciler never talks to ACME or DNS — it only writes
// what it is given, which is what keeps a delegated tenant's cluster free of any
// DNS credential.
func wildcardTLSSecret(certPEM, keyPEM string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      tlsSecretName,
			"namespace": gatewayNS,
			"labels":    sysLabels(nil),
		},
		"type": "kubernetes.io/tls",
		"stringData": map[string]any{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}}
}

// appRoute exposes one app through the shared Gateway: it routes the app's host
// (<app>.<AppsDomain>, or host-less when no domain) to the Service the app
// declares (service : port). Labelled managed (not system) so the reconciler can
// list + GC it alongside the Argo Application.
func appRoute(name, namespace, appID, host, service string, port int) *unstructured.Unstructured {
	if port <= 0 {
		port = 80
	}
	spec := map[string]any{
		"parentRefs": []any{map[string]any{
			"name":      gatewayName,
			"namespace": gatewayNS,
		}},
		"rules": []any{map[string]any{
			"backendRefs": []any{map[string]any{
				"name": service,
				"port": int64(port),
			}},
		}},
	}
	if strings.TrimSpace(host) != "" {
		spec["hostnames"] = []any{host}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				labelManaged: "true",
				labelAppID:   appID,
			},
		},
		"spec": spec,
	}}
}
