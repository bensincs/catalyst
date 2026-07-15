// Package bicep compiles a deployment's Bicep source into an ARM template (JSON)
// at author time, so the reconciler can provision it directly without a Bicep
// toolchain. It shells out to the `bicep` CLI (or `az bicep`) when available.
package bicep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoCompiler is returned when neither `bicep` nor `az bicep` is on PATH.
var ErrNoCompiler = errors.New("no bicep compiler available")

// Compile turns Bicep source into an ARM template (JSON). Source that is already
// an ARM JSON template (starts with `{`) is returned unchanged. Empty source
// yields "". Returns ErrNoCompiler when no toolchain is present, or a build
// error (with the compiler's message) when the Bicep is invalid.
func Compile(ctx context.Context, source string) (string, error) {
	s := strings.TrimSpace(source)
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "{") {
		return s, nil // already an ARM template
	}
	dir, err := os.MkdirTemp("", "cortex-bicep-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "main.bicep")
	out := filepath.Join(dir, "main.json")
	if err := os.WriteFile(in, []byte(source), 0o600); err != nil {
		return "", err
	}
	cmd, err := compileCmd(ctx, in, out)
	if err != nil {
		return "", err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("bicep build failed: %s", strings.TrimSpace(string(b)))
	}
	arm, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	return string(arm), nil
}

// Available reports whether a Bicep toolchain is on PATH.
func Available() bool {
	if _, err := exec.LookPath("bicep"); err == nil {
		return true
	}
	_, err := exec.LookPath("az")
	return err == nil
}

func compileCmd(ctx context.Context, in, out string) (*exec.Cmd, error) {
	if p, err := exec.LookPath("bicep"); err == nil {
		return exec.CommandContext(ctx, p, "build", in, "--outfile", out), nil
	}
	if p, err := exec.LookPath("az"); err == nil {
		return exec.CommandContext(ctx, p, "bicep", "build", "--file", in, "--outfile", out), nil
	}
	return nil, ErrNoCompiler
}
