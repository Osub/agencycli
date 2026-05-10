package formatter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/agencycli/internal/ctxbuild"
)

func TestBuildAgentsMDIncludesCodexRuntimeGuide(t *testing.T) {
	got := buildAgentsMD([]ctxbuild.ContextLayer{
		{Source: "project:test", Content: "# Project"},
	}, nil, "Check queue before doing proactive work.")

	for _, want := range []string{
		"## AgencyCLI Runtime Guide",
		"--project \"${AGENCYCLI_PROJECT}\" --agent \"${AGENCYCLI_AGENT}\"",
		"## Wakeup Routine",
		"Check queue before doing proactive work.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AGENTS.md missing %q\n%s", want, got)
		}
	}
}

func TestCodexFormatterWritesClaudeStyleContextAndSkills(t *testing.T) {
	dir := t.TempDir()
	f := &codexFormatter{}

	err := f.Format(&ctxbuild.MergedContext{
		Layers: []ctxbuild.ContextLayer{
			{Source: "agency", Content: "# Agency"},
			{Source: "project:test", Content: "# Project"},
		},
		Skills: []ctxbuild.SkillDef{
			{
				Name:        "qa-check",
				Description: "Run QA checks",
				Prompt:      "Use {{SKILL_DIR}}/scripts/check.sh",
				Files: []ctxbuild.SkillFile{
					{Name: "scripts/check.sh", Content: []byte("#!/bin/sh\n")},
				},
			},
		},
	}, dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(dir, "AGENTS.md"),
		filepath.Join(dir, ".agencycli", "context", "agency.md"),
		filepath.Join(dir, ".agencycli", "context", "project-test.md"),
		filepath.Join(dir, ".codex", "skills", "qa-check", "SKILL.md"),
		filepath.Join(dir, ".agencycli-skills", "qa-check", "scripts", "check.sh"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), ".codex/skills") {
		t.Fatalf("AGENTS.md does not mention .codex/skills:\n%s", string(agents))
	}
}
