package runner

import (
	"reflect"
	"testing"
)

func TestCodexInvokerAddsProviderModel(t *testing.T) {
	invoker := &codexInvoker{
		addDirs:         []string{"/repo", "/agency"},
		apiModel:        "gpt-5.5",
		reasoningEffort: "medium",
	}

	got := invoker.Args("", "")
	want := []string{
		"codex", "exec", "--skip-git-repo-check", "--sandbox", "workspace-write",
		"--model", "gpt-5.5",
		"--config", `model_reasoning_effort="medium"`,
		"--add-dir", "/repo",
		"--add-dir", "/agency",
		"-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex args mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestCodexInvokerAddsProviderModelWhenResuming(t *testing.T) {
	invoker := &codexInvoker{
		apiModel:        "gpt-5.5",
		reasoningEffort: "medium",
	}

	got := invoker.Args("", "session-123")
	want := []string{
		"codex", "exec", "resume",
		"--model", "gpt-5.5",
		"--config", `model_reasoning_effort="medium"`,
		"session-123",
		"-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex resume args mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestCodexInvokerParsesSessionID(t *testing.T) {
	invoker := &codexInvoker{}
	output := "session id: 019e01cb-2146-79a3-ab34-54e799203721\n\ntokens used\n78,525\n"

	if got := invoker.ParseSessionID(output); got != "019e01cb-2146-79a3-ab34-54e799203721" {
		t.Fatalf("ParseSessionID = %q", got)
	}
}

func TestNormaliseCodexReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"MEDIUM": "medium",
		"xhigh":  "xhigh",
		"fast":   "",
	}
	for input, want := range cases {
		if got := normaliseCodexReasoningEffort(input); got != want {
			t.Fatalf("normaliseCodexReasoningEffort(%q) = %q, want %q", input, got, want)
		}
	}
}
