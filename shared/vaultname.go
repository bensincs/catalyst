package shared

import "strings"

// VaultSecretName is the name of the Key Vault secret holding one key of one
// secret set.
//
// This lives in shared/ and not in either caller because the control plane and
// the reconciler must agree on it EXACTLY, and neither can detect a
// disagreement: the control plane writes through the management plane and
// cannot read back, the reconciler reads through the data plane and never
// writes. A drift between two copies of this function would present as "the
// secret you saved does not exist", with nothing in either log to explain it.
// One implementation makes that class of bug impossible rather than unlikely.
func VaultSecretName(setID, key string) string {
	name := "set-" + vaultNameClean(setID) + "--" + vaultNameClean(key)
	if len(name) > 127 { // Key Vault's limit
		name = name[:127]
	}
	return name
}

// vaultNameClean reduces a string to the Key Vault charset (alphanumerics and
// dashes). The separator is "--" rather than "-" because a single dash is legal
// inside both a set id and a key, so "a-b"+"c" and "a"+"b-c" would otherwise
// name the same secret and silently overwrite each other.
func vaultNameClean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
