package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhg5/agencycli/internal/taskstore"
)

func TestInboxSendAcceptsMessageAliasAndDefaultsAgentSender(t *testing.T) {
	root := setupInboxTestWorkspace(t)
	t.Setenv("AGENCYCLI_PROJECT", "demo")
	t.Setenv("AGENCYCLI_AGENT", "qa")

	oldGlobalDir := globalDir
	globalDir = root
	t.Cleanup(func() { globalDir = oldGlobalDir })

	cmd := newInboxSendCmd()
	cmd.SetArgs([]string{
		"--to", "pm",
		"--message", "hello from qa",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	msgs, err := taskstore.New(root).ListMessages("demo/pm")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if msgs[0].From != "demo/qa" {
		t.Fatalf("from = %q, want demo/qa", msgs[0].From)
	}
	if msgs[0].To != "demo/pm" {
		t.Fatalf("to = %q, want demo/pm", msgs[0].To)
	}
	if msgs[0].Body != "hello from qa" {
		t.Fatalf("body = %q, want hello from qa", msgs[0].Body)
	}
}

func setupInboxTestWorkspace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, ".agencycli"),
		filepath.Join(root, "projects", "demo", "agents", "qa"),
		filepath.Join(root, "projects", "demo", "agents", "pm"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".agencycli", "agency.yaml"), []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
