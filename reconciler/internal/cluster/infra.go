package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/inception42/cortex/shared"

	"sigs.k8s.io/yaml"
)

const infraAPIVersion = "2021-04-01" // Microsoft.Resources/deployments

// applyWiring injects an app's provisioned Bicep outputs into its Helm values at
// the wired paths, so the chart is configured with the address/secret of the
// Azure infra that backs it. Types are preserved (an int output stays an int).
// Pure + defensive: malformed values are left untouched, only existing outputs
// are wired.
func applyWiring(values string, wiring []shared.WireLink, outputs map[string]any) string {
	if len(wiring) == 0 || len(outputs) == 0 {
		return values
	}
	m := map[string]any{}
	if strings.TrimSpace(values) != "" {
		if err := yaml.Unmarshal([]byte(values), &m); err != nil {
			return values // don't corrupt values we can't parse
		}
	}
	changed := false
	for _, w := range wiring {
		v, ok := outputs[w.BicepOutput]
		if !ok || strings.TrimSpace(w.HelmPath) == "" {
			continue
		}
		setNested(m, strings.Split(w.HelmPath, "."), v)
		changed = true
	}
	if !changed {
		return values
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return values
	}
	return string(out)
}

// setNested sets m[a][b][c] = value, creating intermediate maps as needed.
func setNested(m map[string]any, path []string, value any) {
	for i := 0; i < len(path)-1; i++ {
		next, ok := m[path[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[path[i]] = next
		}
		m = next
	}
	if len(path) > 0 {
		m[path[len(path)-1]] = value
	}
}

// Bicep infra states reported back per app.
const (
	infraProvisioning = "provisioning"
	infraReady        = "ready"
	infraFailed       = "failed"
)

// provisionInfra provisions a deployment's compiled ARM template as an ARM
// deployment in the tenant's cluster resource group and returns its outputs +
// state. Idempotent + non-blocking: if the deployment succeeded it returns the
// outputs (state "ready"); if it's absent it submits it ("provisioning"); a
// failed deployment reports "failed". Best-effort — infra never blocks the chart
// from converging with its base values.
func (c *Client) provisionInfra(ctx context.Context, app shared.DesiredApplication) (map[string]any, string) {
	if strings.TrimSpace(app.InfraTemplate) == "" {
		return nil, ""
	}
	name := "cortex-app-" + appName(app.ID)
	if outs, pstate, found := c.deploymentOutputs(ctx, name); found {
		switch {
		case strings.EqualFold(pstate, "Succeeded"):
			return outs, infraReady
		case strings.EqualFold(pstate, "Failed") || strings.EqualFold(pstate, "Canceled"):
			return nil, infraFailed
		default:
			return nil, infraProvisioning
		}
	}
	var template map[string]any
	if err := json.Unmarshal([]byte(app.InfraTemplate), &template); err != nil {
		slog.Warn("app infra: template is not valid ARM JSON; skipping", "app", app.ID)
		return nil, infraFailed
	}
	if err := c.submitDeployment(ctx, name, template); err != nil {
		slog.Warn("app infra: submit deployment failed", "app", app.ID, "err", trunc(err.Error()))
	}
	return nil, infraProvisioning
}

func (c *Client) deploymentURL(name string) string {
	return fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Resources/deployments/%s?api-version=%s",
		c.o.SubscriptionID, c.o.ResourceGroup, name, infraAPIVersion)
}

// deploymentOutputs reads an existing ARM deployment's provisioning state +
// outputs (found=false when the deployment doesn't exist yet). Output values
// keep their JSON type.
func (c *Client) deploymentOutputs(ctx context.Context, name string) (map[string]any, string, bool) {
	var body struct {
		Properties struct {
			ProvisioningState string                         `json:"provisioningState"`
			Outputs           map[string]struct{ Value any } `json:"outputs"`
		} `json:"properties"`
	}
	if err := c.arm(ctx, http.MethodGet, c.deploymentURL(name), &body); err != nil {
		return nil, "", false
	}
	outs := make(map[string]any, len(body.Properties.Outputs))
	for k, v := range body.Properties.Outputs {
		outs[k] = v.Value
	}
	return outs, body.Properties.ProvisioningState, true
}

// submitDeployment PUTs an incremental ARM deployment of the given template.
func (c *Client) submitDeployment(ctx context.Context, name string, template map[string]any) error {
	payload, err := json.Marshal(map[string]any{
		"properties": map[string]any{"mode": "Incremental", "template": template},
	})
	if err != nil {
		return err
	}
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return fmt.Errorf("acquire ARM token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.deploymentURL(name), bytes.NewReader(payload))
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
		return fmt.Errorf("submit deployment: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
