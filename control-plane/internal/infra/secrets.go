package infra

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/inception42/cortex/shared"
)

// Writing a tenant's secret values into the tenant's own Key Vault.
//
// The whole design rests on one asymmetry of the Azure API surface: the ARM
// management plane can CREATE a secret (`PUT .../vaults/{v}/secrets/{n}`) but
// has no operation that returns a secret's value. Reading requires the vault's
// data plane, on a hostname the control plane deliberately holds no credential
// for. So this file can accept a secret from a tenant and put it beyond its own
// reach in the same call.
//
// That is why values never touch the database, never appear on the sync, and are
// not recoverable by a platform administrator. The reconciler reads them,
// because it runs inside the tenant's own subscription and its managed identity
// holds Key Vault Secrets User on the tenant's vault — an ordinary
// same-directory grant, made by the footprint that creates the vault.

// WriteTenantSecrets stores values in a tenant's own vault and returns the keys
// successfully written.
//
// Partial success is reported rather than hidden: the caller records exactly the
// keys that landed, so a set with one failed key shows that key as still
// outstanding instead of appearing complete and then failing in the cluster.
//
// The values are not logged, not returned, and not retained — including on the
// error path, where only the key name is mentioned.
func (p *Provisioner) WriteTenantSecrets(ctx context.Context, vaultID, setID string, values map[string]string) ([]string, error) {
	if strings.TrimSpace(vaultID) == "" {
		return nil, fmt.Errorf("tenant has no vault yet — its footprint has not finished provisioning")
	}
	var written []string
	var failed []string
	for key, val := range values {
		name := shared.VaultSecretName(setID, key)
		if err := p.armJSON(ctx, "PUT", fmt.Sprintf(
			"https://management.azure.com%s/secrets/%s?api-version=2023-07-01", vaultID, name),
			map[string]any{"properties": map[string]any{"value": val}}, nil); err != nil {
			// The key, never the value.
			slog.Warn("secrets: write failed", "set", setID, "key", key, "err", trunc(err.Error()))
			failed = append(failed, key)
			continue
		}
		written = append(written, key)
	}
	if len(failed) > 0 {
		return written, fmt.Errorf("could not store %s", strings.Join(failed, ", "))
	}
	return written, nil
}

// Deleting a tenant's secret values is deliberately NOT implemented here, and
// cannot be: the ARM management plane answers DeleteNotSupported for a Key Vault
// secret (verified against the live API — the write succeeds and the delete does
// not), so removal requires the vault's data plane, which the control plane has
// no credential for.
//
// An earlier version of this called armDelete and logged a warning on failure,
// which meant disabling a secret set reported success while the values stayed
// exactly where they were. Claiming to destroy a secret and not doing so is
// worse than not offering it, so the operation is gone and the product says what
// actually happens instead: delivery stops, and the values remain in the
// TENANT's own vault, where only the tenant can remove them.
//
// The reconciler could delete them — it holds a data-plane role — but only with
// Key Vault Secrets Officer instead of Secrets User, which would let anything
// that compromised the cluster destroy the tenant's credentials. Read-only there
// is worth more than automatic cleanup here.

// recordVault stores the tenant's vault coordinates from its footprint outputs,
// so the control plane knows where to write and the reconciler where to read.
func (p *Provisioner) recordVault(ctx context.Context, slug string, outs map[string]any) {
	str := func(k string) string {
		v, _ := outs[k].(string)
		return strings.TrimSpace(v)
	}
	name, uri, id := str("vaultName"), str("vaultUri"), str("vaultId")
	if name == "" || uri == "" || id == "" {
		return // footprint predates the vault; nothing to record
	}
	if err := p.store.SetTenantVault(ctx, slug, name, uri, id); err != nil {
		slog.Warn("secrets: record vault failed", "tenant", slug, "err", trunc(err.Error()))
	}
}

// recordNetwork stores the private-networking facts from the footprint outputs.
// Separate from recordVault because they appear at different times: the vault
// exists on the first stamp, the networking only once the cluster does.
func (p *Provisioner) recordNetwork(ctx context.Context, slug string, outs map[string]any) {
	str := func(k string) string {
		v, _ := outs[k].(string)
		return strings.TrimSpace(v)
	}
	pe, issuer, rg := str("privateEndpointSubnetId"), str("aksOidcIssuerUrl"), str("privateDnsZoneResourceGroup")
	if pe == "" && issuer == "" {
		return
	}
	if err := p.store.SetTenantNetwork(ctx, slug, pe, issuer, rg); err != nil {
		slog.Warn("network: record failed", "tenant", slug, "err", trunc(err.Error()))
	}
}
