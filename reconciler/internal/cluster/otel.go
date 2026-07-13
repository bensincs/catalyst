package cluster

import (
	"fmt"
	"strings"
)

// Grafana Alloy is deployed to every cluster as an OpenTelemetry collector: a
// single OTLP endpoint (gRPC 4317 / HTTP 4318) any workload — and the mesh
// itself — emits traces, metrics, and logs to. It runs in its own namespace,
// outside the mesh, so it's reachable before sidecars are ready and isn't
// subject to the ingress JWT policy. It ships to OTelExporterEndpoint, or logs
// locally when none is set (collect-only, never a fabricated backend).
const (
	observabilityNS  = "observability"
	alloyRepo        = "https://grafana.github.io/helm-charts"
	alloyServiceName = "alloy" // fullnameOverride ⇒ stable Service name
	otlpGRPCPort     = 4317
	otlpHTTPPort     = 4318
)

// alloyValues renders the Alloy Helm values: a hardened, non-root, read-only
// collector with a stable Service name and the OTLP ports exposed.
func alloyValues(otlpEndpoint string) string {
	return fmt.Sprintf(`fullnameOverride: %s
controller:
  type: deployment
  replicas: 1
  podSecurityContext:
    runAsNonRoot: true
    runAsUser: 473
    runAsGroup: 473
    fsGroup: 473
    seccompProfile:
      type: RuntimeDefault
rbac:
  create: true
serviceAccount:
  create: true
alloy:
  configMap:
    content: |-
%s
  extraPorts:
  - name: otlp-grpc
    port: %d
    targetPort: %d
    protocol: TCP
  - name: otlp-http
    port: %d
    targetPort: %d
    protocol: TCP
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: "1"
      memory: 512Mi
  securityContext:
    allowPrivilegeEscalation: false
    readOnlyRootFilesystem: true
    runAsNonRoot: true
    runAsUser: 473
    capabilities:
      drop:
      - ALL
    seccompProfile:
      type: RuntimeDefault
`, alloyServiceName, indent(alloyConfig(otlpEndpoint), 6), otlpGRPCPort, otlpGRPCPort, otlpHTTPPort, otlpHTTPPort)
}

// alloyConfig is the River pipeline: receive OTLP over gRPC + HTTP, batch, and
// export to the configured OTLP endpoint over TLS — or to the debug sink (logs)
// when no endpoint is set, so telemetry is still collected without inventing a
// destination.
func alloyConfig(otlpEndpoint string) string {
	var exporter, ref string
	if strings.TrimSpace(otlpEndpoint) != "" {
		ref = "otelcol.exporter.otlp.default.input"
		exporter = fmt.Sprintf(`otelcol.exporter.otlp "default" {
  client {
    endpoint = %q
    tls {
      insecure = false
    }
  }
}`, otlpEndpoint)
	} else {
		ref = "otelcol.exporter.debug.default.input"
		exporter = `otelcol.exporter.debug "default" {
  verbosity = "basic"
}`
	}
	return fmt.Sprintf(`logging {
  level  = "info"
  format = "logfmt"
}

otelcol.receiver.otlp "default" {
  grpc {
    endpoint = "0.0.0.0:%d"
  }
  http {
    endpoint = "0.0.0.0:%d"
  }
  output {
    metrics = [otelcol.processor.batch.default.input]
    logs    = [otelcol.processor.batch.default.input]
    traces  = [otelcol.processor.batch.default.input]
  }
}

otelcol.processor.batch "default" {
  output {
    metrics = [%s]
    logs    = [%s]
    traces  = [%s]
  }
}

%s
`, otlpGRPCPort, otlpHTTPPort, ref, ref, ref, exporter)
}

// indent prefixes every line with n spaces (for embedding a block scalar in the
// Helm values YAML). Blank lines are left empty rather than space-padded.
func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		lines[i] = pad + ln
	}
	return strings.Join(lines, "\n")
}
