package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/control-plane/internal/store"
)

// Accepting a tenant's secret values.
//
// This is the only place in the product that takes a secret from a user, and it
// is deliberately a one-way door. The value goes to the tenant's OWN Key Vault
// through the ARM management plane, which offers no way to read a secret back —
// so by the time this handler returns, the platform can no longer see what it
// was given. Nothing is written to the database but the KEY NAMES.
//
// Two consequences worth stating, because they are design intent rather than
// limitations to be fixed later:
//
//   - A platform administrator cannot read a tenant's secrets. There is no
//     endpoint, no admin override, and no recovery path.
//   - A tenant that loses a value cannot retrieve it either; it can only replace
//     it. Re-enabling with a subset of keys updates only those keys.

// SecretWriter stores secret values in a tenant's own vault. Satisfied by the
// infra provisioner, which owns the ARM credential; an interface so the API does
// not depend on it being present (it is nil when cross-tenant provisioning is
// off, in which case there are no tenant vaults to write to).
type SecretWriter interface {
	WriteTenantSecrets(ctx context.Context, vaultID, setID string, values map[string]string) ([]string, error)
	DeleteTenantSecrets(ctx context.Context, vaultID, setID string, keys []string)
}

// SetSecretWriter wires tenant-vault access, which the provisioner owns.
func (s *Server) SetSecretWriter(m SecretWriter) { s.secrets = m }

// enableSecretSet turns a secret set on for the caller's tenant and stores the
// values it supplied.
//
// Order matters and is not incidental: the vault write happens FIRST, and only
// the keys that actually landed are recorded. Recording first would produce a
// set that claims to be complete while the cluster finds nothing to read, which
// is the failure mode this ordering exists to prevent.
func (s *Server) enableSecretSet(w http.ResponseWriter, r *http.Request, slug, rid string) error {
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := decodeJSONOptional(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return nil
	}

	// Reject undeclared keys rather than storing them. A key the author never
	// declared can never be delivered to a chart, so accepting it would put a
	// secret in the vault that nothing will ever read and nobody will remember.
	declared, err := s.store.DeclaredKeys(r.Context(), rid)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(declared))
	for _, k := range declared {
		allowed[k] = true
	}
	values := map[string]string{}
	var undeclared []string
	for k, v := range body.Values {
		k = strings.TrimSpace(k)
		switch {
		case k == "":
			continue
		case !allowed[k]:
			undeclared = append(undeclared, k)
		case strings.TrimSpace(v) == "":
			// An empty value is "leave this one alone", not "store an empty
			// secret" — the same convention the OIDC client secret uses, so
			// re-saving a form without re-typing every value is non-destructive.
			continue
		default:
			values[k] = v
		}
	}
	if len(undeclared) > 0 {
		writeErr(w, http.StatusBadRequest, "not declared by this secret set: "+strings.Join(undeclared, ", "))
		return nil
	}

	var written []string
	if len(values) > 0 {
		if s.secrets == nil {
			writeErr(w, http.StatusServiceUnavailable,
				"secret storage is unavailable — cross-tenant provisioning is off, so this tenant has no vault")
			return nil
		}
		vaultID, _, err := s.store.TenantVault(r.Context(), slug)
		if err != nil {
			return err
		}
		if strings.TrimSpace(vaultID) == "" {
			writeErr(w, http.StatusConflict,
				"this tenant has no vault yet — its footprint is still provisioning")
			return nil
		}
		written, err = s.secrets.WriteTenantSecrets(r.Context(), vaultID, rid, values)
		if err != nil {
			// Record whatever landed before reporting the failure, so a partial
			// write is not silently rolled back into "nothing was set".
			if len(written) > 0 {
				_, uri, _ := s.store.TenantVault(r.Context(), slug)
				_ = s.store.EnableSecretSet(r.Context(), slug, rid, written, uri)
			}
			writeErr(w, http.StatusBadGateway, err.Error())
			return nil
		}
	}

	_, vaultURI, err := s.store.TenantVault(r.Context(), slug)
	if err != nil {
		return err
	}
	return s.store.EnableSecretSet(r.Context(), slug, rid, written, vaultURI)
}

// disableSecretSet turns a set off and reclaims its values from the tenant's
// vault. This is the only path that deletes them: an incidental prune leaves
// them alone, because the control plane cannot read a value back and so could
// not restore one it removed by mistake.
func (s *Server) disableSecretSet(ctx context.Context, slug, rid string) error {
	keys, err := s.store.SecretSetKeysSet(ctx, slug, rid)
	if err != nil {
		return err
	}
	if err := s.store.DisableSecretSet(ctx, slug, rid); err != nil {
		return err
	}
	if s.secrets != nil && len(keys) > 0 {
		if vaultID, _, err := s.store.TenantVault(ctx, slug); err == nil && vaultID != "" {
			s.secrets.DeleteTenantSecrets(ctx, vaultID, rid, keys)
		}
	}
	return nil
}

// secretSetWriteAllowed authorises editing or deleting a secret set: the
// platform owns platform-authored sets, a tenant owns its own.
func (s *Server) secretSetWriteAllowed(w http.ResponseWriter, r *http.Request, id model.Identity, rid string) (string, bool) {
	set, err := s.store.SecretSetByID(r.Context(), rid)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "secret set not found")
		return "", false
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	return s.ownerWrite(w, r, id, set.Owner, "secret set")
}
