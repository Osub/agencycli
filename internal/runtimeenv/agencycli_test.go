package runtimeenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithAgencycliEnvUsesWorkspaceDistBinary(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "dist", "agencycli")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	env := WithAgencycliEnv([]string{"PATH=/usr/bin"}, root)
	wantBin := mustRealPath(t, bin)
	if got := envValue(env, AgencycliBinEnv); got != wantBin {
		t.Fatalf("AGENCYCLI_BIN: got %q, want %q", got, wantBin)
	}
	path := envValue(env, "PATH")
	if !strings.HasPrefix(path, filepath.Dir(wantBin)+string(os.PathListSeparator)) {
		t.Fatalf("PATH %q does not start with binary dir", path)
	}
	if got := envValue(env, AgencycliRootEnv); got != root {
		t.Fatalf("AGENCYCLI_ROOT: got %q, want %q", got, root)
	}
}

func TestWithAgencycliEnvRespectsExistingBinary(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "custom-agencycli")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	env := WithAgencycliEnv([]string{"PATH=/usr/bin", "AGENCYCLI_BIN=" + bin}, root)
	wantBin := mustRealPath(t, bin)
	if got := envValue(env, AgencycliBinEnv); got != wantBin {
		t.Fatalf("AGENCYCLI_BIN: got %q, want %q", got, wantBin)
	}
}

func mustRealPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
