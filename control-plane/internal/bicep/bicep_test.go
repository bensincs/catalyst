package bicep

import (
	"context"
	"strings"
	"testing"
)

func TestCompilePassthroughAndEmpty(t *testing.T) {
	// An ARM JSON template is returned unchanged (no toolchain needed).
	arm := `{"$schema":"x","resources":[]}`
	if got, err := Compile(context.Background(), arm); err != nil || got != arm {
		t.Fatalf("ARM passthrough: got %q err %v", got, err)
	}
	if got, err := Compile(context.Background(), "  "); err != nil || got != "" {
		t.Fatalf("empty source: got %q err %v", got, err)
	}
}

func TestCompileBicep(t *testing.T) {
	if !Available() {
		t.Skip("no bicep toolchain on PATH")
	}
	arm, err := Compile(context.Background(), "output greeting string = 'hello'\n")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(arm, `"outputs"`) || !strings.Contains(arm, "greeting") {
		t.Fatalf("compiled ARM missing the output: %s", arm)
	}
}
