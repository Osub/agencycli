package runtimeenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	AgencycliBinEnv  = "AGENCYCLI_BIN"
	AgencycliRootEnv = "AGENCYCLI_ROOT"
)

// AgencycliBinaryPath returns the best absolute path to the agencycli binary.
// It intentionally prefers the current process when it really is agencycli, but
// falls back to <root>/dist/agencycli for daemon/web runs started with a narrow
// PATH.
func AgencycliBinaryPath(root string, env []string) string {
	if p := cleanExecutablePath(envValue(env, AgencycliBinEnv)); p != "" {
		return p
	}

	if p := currentExecutable(); p != "" && filepath.Base(p) == "agencycli" {
		return p
	}

	if root != "" {
		if p := cleanExecutablePath(filepath.Join(root, "dist", "agencycli")); p != "" {
			return p
		}
	}

	if p, err := exec.LookPath("agencycli"); err == nil {
		if cleaned := cleanExecutablePath(p); cleaned != "" {
			return cleaned
		}
	}

	return currentExecutable()
}

// WithAgencycliEnv injects stable callback env vars for subprocesses that may
// execute shell commands through a daemon-launched PATH.
func WithAgencycliEnv(base []string, root string) []string {
	out := append([]string(nil), base...)
	if root = strings.TrimSpace(root); root != "" {
		out = setEnv(out, AgencycliRootEnv, root)
	}

	bin := AgencycliBinaryPath(root, out)
	if bin == "" {
		return out
	}

	out = setEnv(out, AgencycliBinEnv, bin)
	if dir := filepath.Dir(bin); dir != "" && dir != "." {
		out = setEnv(out, "PATH", prependPath(envValue(out, "PATH"), dir))
	}
	return out
}

func currentExecutable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return cleanExecutablePath(p)
}

func cleanExecutablePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}

func prependPath(pathValue, dir string) string {
	if pathValue == "" {
		return dir
	}
	parts := filepath.SplitList(pathValue)
	for _, p := range parts {
		if p == dir {
			return pathValue
		}
	}
	return dir + string(os.PathListSeparator) + pathValue
}

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}

func setEnv(env []string, key, value string) []string {
	for i, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if ok && k == key {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}
