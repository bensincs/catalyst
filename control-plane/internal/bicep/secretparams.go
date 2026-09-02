package bicep

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Binding a Bicep parameter to a secret, without the control plane ever learning
// the value.
//
// The obvious implementation — read the secret and pass it as a parameter — is
// not available, and deliberately so: the control plane writes tenant secrets
// through the ARM management plane precisely because that plane cannot read them
// back. So it has no way to supply the value itself.
//
// ARM's own Key Vault parameter reference solves this exactly. Instead of a
// value, the deployment's parameters carry a POINTER to a vault secret, and ARM
// resolves it internally at deploy time. The control plane needs
// Microsoft.KeyVault/vaults/deploy/action on the vault, which is an action
// permission rather than a read one — it authorises "use this in a deployment"
// and grants no ability to fetch the value. The secret goes from the vault into
// the deployment without passing through this process at all.
//
// This is also what makes the parameter genuinely secret in Azure: a value baked
// into the template as a literal is preserved in the deployment history forever
// and readable by anyone with reader access on the resource group. A @secure()
// parameter fed from a vault reference is not recorded at all.

// SecretRefKey is the marker in an author's params identifying a value that
// should be bound to a secret set rather than baked into the template.
const SecretRefKey = "$secret"

// SecretBinding is one Bicep parameter fed from a secret set's key.
type SecretBinding struct {
	Param string `json:"param"` // the module's parameter name
	SetID string `json:"setId"` // secret set id
	Key   string `json:"key"`   // key within that set
}

// splitSecretParams separates author params into ordinary values (baked as
// literals, as before) and secret bindings (declared as @secure() parameters and
// supplied at deploy time).
//
// A binding is written as {"$secret": {"setId": "...", "key": "..."}}. Malformed
// markers are left as ordinary values rather than silently dropped, so an author
// sees the resulting Bicep compile error instead of a parameter that quietly
// vanished.
func splitSecretParams(params map[string]any) (plain map[string]any, secrets []SecretBinding) {
	plain = map[string]any{}
	for k, v := range params {
		b, ok := asSecretBinding(k, v)
		if !ok {
			plain[k] = v
			continue
		}
		secrets = append(secrets, b)
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Param < secrets[j].Param })
	return plain, secrets
}

func asSecretBinding(param string, v any) (SecretBinding, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return SecretBinding{}, false
	}
	inner, ok := m[SecretRefKey].(map[string]any)
	if !ok {
		return SecretBinding{}, false
	}
	setID, _ := inner["setId"].(string)
	key, _ := inner["key"].(string)
	setID, key = strings.TrimSpace(setID), strings.TrimSpace(key)
	if setID == "" || key == "" {
		return SecretBinding{}, false
	}
	return SecretBinding{Param: param, SetID: setID, Key: key}, true
}

// secureParamDecls renders the @secure() parameter declarations for the wrapper,
// and the entries that pass them through to the module.
func secureParamDecls(secrets []SecretBinding) (decls string, passthrough map[string]any) {
	if len(secrets) == 0 {
		return "", nil
	}
	var b strings.Builder
	passthrough = map[string]any{}
	for _, s := range secrets {
		fmt.Fprintf(&b, "@secure()\nparam %s string\n", s.Param)
		// rawBicep so the value is emitted as an identifier reference rather
		// than a quoted string literal — the whole point is that no literal
		// exists.
		passthrough[s.Param] = rawBicep(s.Param)
	}
	b.WriteString("\n")
	return b.String(), passthrough
}

// rawBicep is a value emitted into Bicep verbatim (an expression), not quoted as
// a string literal.
type rawBicep string

// KeyVaultParameters renders the ARM `parameters` object binding each secret
// parameter to a vault secret. The values are references; no secret is present.
func KeyVaultParameters(vaultID string, secrets []SecretBinding, secretName func(setID, key string) string) map[string]any {
	if vaultID == "" || len(secrets) == 0 {
		return nil
	}
	out := make(map[string]any, len(secrets))
	for _, s := range secrets {
		out[s.Param] = map[string]any{
			"reference": map[string]any{
				"keyVault":   map[string]any{"id": vaultID},
				"secretName": secretName(s.SetID, s.Key),
			},
		}
	}
	return out
}

// MarshalBindings serialises bindings for storage.
func MarshalBindings(b []SecretBinding) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	out, err := json.Marshal(b)
	if err != nil {
		return []byte("[]")
	}
	return out
}

// UnmarshalBindings reads stored bindings.
func UnmarshalBindings(raw []byte) []SecretBinding {
	out := []SecretBinding{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}
