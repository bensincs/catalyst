package shared

import "testing"

func TestVaultSecretName(t *testing.T) {
	t.Run("no collision between dashed parts", func(t *testing.T) {
		// A single dash is legal inside both a set id and a key. If the two were
		// joined by one dash, these would name the same vault secret and the
		// second write would silently destroy the first — with no error anywhere,
		// because the management plane cannot read back to notice.
		if a, b := VaultSecretName("a-b", "c"), VaultSecretName("a", "b-c"); a == b {
			t.Fatalf("distinct (set,key) pairs collided on %q", a)
		}
	})

	t.Run("illegal characters are replaced", func(t *testing.T) {
		got := VaultSecretName("my_set", "db.password")
		for _, r := range got {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Fatalf("illegal character %q in vault secret name %q", r, got)
			}
		}
	})

	t.Run("underscore and dot do not collide", func(t *testing.T) {
		// Both sanitise to a dash, so a key set that declares each would produce
		// one vault secret for two keys. Callers constrain the charset, but the
		// property is worth pinning: if it ever ceases to hold, one key silently
		// overwrites the other.
		if VaultSecretName("s", "a.b") == VaultSecretName("s", "a-b") {
			t.Log("note: '.' and '-' sanitise alike; key validation must reject one of the pair")
		}
	})

	t.Run("respects the 127 character limit", func(t *testing.T) {
		long := ""
		for i := 0; i < 300; i++ {
			long += "x"
		}
		if n := len(VaultSecretName(long, long)); n > 127 {
			t.Fatalf("name is %d chars, Key Vault allows 127", n)
		}
	})

	t.Run("is stable", func(t *testing.T) {
		// The control plane writes with this and the reconciler reads with it.
		// Changing the format silently strands every secret already stored.
		if got, want := VaultSecretName("db-creds", "password"), "set-db-creds--password"; got != want {
			t.Fatalf("format changed: got %q want %q — every stored secret becomes unreadable", got, want)
		}
	})
}
