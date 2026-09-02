package cluster

import (
	"encoding/base64"
	"testing"
)

// A removed key must actually disappear from the cluster.
//
// The Secret was first written with stringData, which is write-only: the API
// server folds it into data and clears it, so server-side apply recorded
// ownership of a field that does not persist. Removing a key dropped it from the
// apply's ownership set and left the value in data indefinitely — so revoking a
// key told the author it was gone while the cluster kept serving it. Owning
// `data` directly is what makes the removal real.
func TestSecretUsesDataNotStringData(t *testing.T) {
	obj := secretSetSecret("cortex-secret-db", "ns", "db", map[string]string{"password": "s3cr3t"})

	if _, bad := obj.Object["stringData"]; bad {
		t.Fatal("stringData is write-only — SSA cannot prune a key removed from it")
	}
	data, ok := obj.Object["data"].(map[string]any)
	if !ok {
		t.Fatal("no data field, so the Secret carries nothing")
	}
	enc, ok := data["password"].(string)
	if !ok {
		t.Fatal("key missing from data")
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("data must be base64 when written to `data`: %v", err)
	}
	if string(raw) != "s3cr3t" {
		t.Fatalf("value round-tripped wrong: %q", raw)
	}
}

func TestSecretCarriesOnlyTheKeysGiven(t *testing.T) {
	// The applied field set is what SSA prunes against, so it must contain
	// exactly the delivered keys and nothing else.
	obj := secretSetSecret("cortex-secret-db", "ns", "db", map[string]string{"password": "p"})
	data := obj.Object["data"].(map[string]any)
	if len(data) != 1 {
		t.Fatalf("expected exactly the delivered keys, got %v", data)
	}
	if _, stale := data["werwe"]; stale {
		t.Fatal("a key that was not delivered appeared in the Secret")
	}
}

func TestSecretIsLabelledForItsOwnPrune(t *testing.T) {
	// The generic GC selects labelManaged=true,labelSystem!=true and these carry
	// labelSystem, so they match no existing cleanup path. pruneSecretSets keys
	// on the secret-set label instead — without it a disabled store leaves live
	// credentials in the namespace.
	obj := secretSetSecret("cortex-secret-db", "ns", "db", map[string]string{"password": "p"})
	meta := obj.Object["metadata"].(map[string]any)
	labels := meta["labels"].(map[string]any)
	if labels[labelSecretSet] != "db" {
		t.Fatalf("missing the label its prune selects on: %v", labels)
	}
}
