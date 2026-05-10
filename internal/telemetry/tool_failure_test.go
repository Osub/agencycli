package telemetry

import (
	"strings"
	"testing"
)

func TestDetectModelToolFailure_ExitedNonZero(t *testing.T) {
	data := []byte("exec\n/bin/zsh -lc 'forge test --match-contract A --match-contract B'\n exited 2 in 12ms:\nerror: the argument '--match-contract <REGEX>' cannot be used multiple times\n")
	f, ok := DetectModelToolFailure(data)
	if !ok {
		t.Fatal("expected tool failure")
	}
	if !strings.Contains(f.Message, "--match-contract") {
		t.Fatalf("message does not include context: %q", f.Message)
	}
}

func TestDetectModelToolFailure_CommandNotFound(t *testing.T) {
	data := []byte("zsh:1: command not found: agencycli\n exited 127 in 0ms:\nzsh:1: command not found: agencycli\n")
	f, ok := DetectModelToolFailure(data)
	if !ok {
		t.Fatal("expected tool failure")
	}
	if !strings.Contains(f.Message, "command not found") {
		t.Fatalf("message does not include command failure: %q", f.Message)
	}
}

func TestDetectModelToolFailure_AllowsAgencycliCallback(t *testing.T) {
	data := []byte("exec\n/bin/zsh -lc '\"$AGENCYCLI_BIN\" task done --id t-1 --status success'\n exited 1 in 2ms:\ntask not found\n")
	if f, ok := DetectModelToolFailure(data); ok {
		t.Fatalf("unexpected failure: %+v", f)
	}
}

func TestDetectAgencycliTaskTerminalStatus(t *testing.T) {
	if got := DetectAgencycliTaskTerminalStatus([]byte("✓ Task t-1 marked done_success\n")); got != "done_success" {
		t.Fatalf("got %q, want done_success", got)
	}
	if got := DetectAgencycliTaskTerminalStatus([]byte("✓ Task t-1 marked done_failed\n")); got != "done_failed" {
		t.Fatalf("got %q, want done_failed", got)
	}
}
