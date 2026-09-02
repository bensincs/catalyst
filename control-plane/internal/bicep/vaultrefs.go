package bicep

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Finding the Key Vault secrets a template needs before it is deployed.
//
// A module can resolve a credential itself rather than being handed one:
// `vault.getSecret(name)` compiles to a Key Vault REFERENCE in a nested
// deployment's parameters, and ARM fetches the value during the deployment. That
// keeps the secret out of the template entirely, which is the point.
//
// The cost is a failure mode. If the secret is not in the vault yet — the tenant
// has not supplied it — ARM fails the whole deployment with an error about a
// resource it could not read, which says nothing about the value somebody owes.
// Reading the names out of the template up front means the deployment can be
// held with a reason instead.
//
// Only names are extracted. The control plane cannot read a tenant's vault, and
// does not need to: it checks that a secret EXISTS, which the management plane
// answers without returning the value.

// armParamRef matches an ARM expression that is exactly one parameter lookup,
// e.g. [parameters('passwordSecretName')].
var armParamRef = regexp.MustCompile(`^\[\s*parameters\(\s*'([^']+)'\s*\)\s*\]$`)

// VaultSecretRefs returns the vault secret names a compiled ARM template reads
// via Key Vault references, resolved as far as the template allows.
//
// A reference's secretName is usually an expression pointing at a parameter of
// the nested deployment, whose value the enclosing deployment supplies as a
// literal — that chain is followed. A name that cannot be resolved statically
// (built from a runtime expression) is skipped rather than guessed: reporting a
// wrong name would be worse than reporting none.
func VaultSecretRefs(arm string) []string {
	var doc any
	if json.Unmarshal([]byte(arm), &doc) != nil {
		return nil
	}
	found := map[string]bool{}
	collectVaultRefs(doc, nil, found)
	out := make([]string, 0, len(found))
	for n := range found {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// collectVaultRefs walks the template. `scopes` is the stack of parameter maps
// of the enclosing deployments, innermost last, so a `[parameters('x')]` in a
// nested deployment can be resolved against what its parent passed in.
func collectVaultRefs(node any, scopes []map[string]any, found map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		// Entering a deployment's template: everything below it resolves against
		// the parameters this deployment was given.
		if props, ok := n["properties"].(map[string]any); ok {
			if params, ok := props["parameters"].(map[string]any); ok {
				if _, isTemplate := props["template"]; isTemplate {
					scopes = append(scopes, params)
				}
			}
		}
		if ref, ok := n["reference"].(map[string]any); ok {
			if _, isKV := ref["keyVault"]; isKV {
				if name, ok := resolveName(ref["secretName"], scopes); ok {
					found[name] = true
				}
			}
		}
		for _, v := range n {
			collectVaultRefs(v, scopes, found)
		}
	case []any:
		for _, v := range n {
			collectVaultRefs(v, scopes, found)
		}
	}
}

// resolveName turns a reference's secretName into a literal, following one
// parameter indirection per enclosing scope.
func resolveName(v any, scopes []map[string]any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for range scopes { // bounded: at most one hop per enclosing deployment
		if !strings.HasPrefix(s, "[") {
			return s, s != ""
		}
		m := armParamRef.FindStringSubmatch(s)
		if m == nil {
			return "", false // an expression we cannot evaluate
		}
		next, ok := lookupParam(m[1], scopes)
		if !ok {
			return "", false
		}
		s = next
	}
	if strings.HasPrefix(s, "[") {
		return "", false
	}
	return s, s != ""
}

// lookupParam finds a parameter's literal value, innermost scope first.
func lookupParam(name string, scopes []map[string]any) (string, bool) {
	for i := len(scopes) - 1; i >= 0; i-- {
		entry, ok := scopes[i][name].(map[string]any)
		if !ok {
			continue
		}
		if lit, ok := entry["value"].(string); ok {
			return lit, true
		}
		if lit, ok := entry["defaultValue"].(string); ok {
			return lit, true
		}
	}
	return "", false
}
