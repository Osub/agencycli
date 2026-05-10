package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chenhg5/agencycli/internal/entity"
	"github.com/chenhg5/agencycli/internal/runner"
	"github.com/chenhg5/agencycli/internal/store"
	"github.com/chenhg5/agencycli/internal/taskstore"
	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	var (
		project   string
		agentName string
		prompt    string
		file      string
		sessionID string
		noSession bool
		compareOV bool
	)

	cmd := &cobra.Command{
		Use:   "exec",
		Short: "Run a prompt against an agent directly (bypasses task queue)",
		Long: `Run a raw prompt against an agent immediately, without creating a task.

Useful for quick interactive testing or one-off commands.
Output is streamed to stdout in real time and also written to a log file.
The session ID is saved automatically so the next exec resumes the same
conversation (use --no-session to start fresh).

Examples:

  # Inline prompt
  agencycli exec --project cc-connect --agent pm \
    --prompt "List all open GitHub issues and summarize them"

  # From a file
  agencycli exec --project cc-connect --agent dev-claude --file task.txt

  # From stdin (pipe)
  echo "What is 1+1?" | agencycli exec --project cc-connect --agent pm

  # From stdin (explicit)
  agencycli exec --project cc-connect --agent pm - <<'EOF'
  Check the latest PRs.
  EOF

  # Resume a specific session
  agencycli exec --project cc-connect --agent pm \
    --prompt "Prioritize those bugs" --session abc123

  # Start a fresh conversation (ignore saved session)
  agencycli exec --project cc-connect --agent pm \
    --prompt "Start over" --no-session

  # Compare OpenViking off vs on for the same prompt
  agencycli exec --project cc-connect --agent pm \
    --prompt "Summarize recent context" --compare-openviking`,
		Args: cobra.MaximumNArgs(1), // optional "-" for explicit stdin
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot()
			if err != nil {
				return err
			}
			if project == "" || agentName == "" {
				return fmt.Errorf("--project and --agent are required")
			}

			promptText, err := resolveExecPrompt(prompt, file, args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(promptText) == "" {
				return fmt.Errorf("prompt is empty — use --prompt TEXT, --file PATH, or pipe via stdin")
			}

			ts := taskstore.New(root)

			// Resolve session: --session flag > saved heartbeat session > "".
			sid := sessionID
			if !noSession && sid == "" {
				if hb, err := ts.GetHeartbeat(project, agentName); err == nil && hb.SessionID != "" {
					sid = hb.SessionID
					fmt.Fprintf(os.Stderr, "↩  resuming session %s  (--no-session to start fresh)\n\n", sid)
				}
			}

			as := store.NewFS(root)
			r := runner.New(root, ts, as)

			if compareOV {
				return runExecOpenVikingCompare(cmd, root, r, project, agentName, promptText)
			}

			fmt.Fprintf(os.Stderr, "▶  exec %s/%s\n\n", project, agentName)

			result, err := r.ExecPrompt(project, agentName, promptText, sid)
			if err != nil {
				return fmt.Errorf("exec: %w", err)
			}

			fmt.Fprintf(os.Stderr, "\n── exec complete ─────────────────────────────────\n")
			fmt.Fprintf(os.Stderr, "status  : %s\n", result.Status)
			fmt.Fprintf(os.Stderr, "log     : %s\n", result.LogPath)
			if result.SessionID != "" {
				fmt.Fprintf(os.Stderr, "session : %s\n", result.SessionID)
				// Auto-save so the next exec resumes this conversation.
				if !noSession && result.Status != entity.TaskStatusDoneFailed {
					if hb, err2 := ts.GetHeartbeat(project, agentName); err2 == nil {
						hb.SessionID = result.SessionID
						if err2 = ts.SaveHeartbeat(project, agentName, hb); err2 == nil {
							fmt.Fprintf(os.Stderr, "         (saved — next exec resumes automatically)\n")
						}
					}
				}
			}
			if result.ErrorMsg != "" {
				fmt.Fprintf(os.Stderr, "error   : %s\n", result.ErrorMsg)
			}
			fmt.Fprintf(os.Stderr, "──────────────────────────────────────────────────\n")

			if result.Status == entity.TaskStatusDoneFailed {
				return fmt.Errorf("agent exited with error")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name")
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "prompt text (inline)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read prompt from file")
	cmd.Flags().StringVar(&sessionID, "session", "", "session ID to resume (overrides saved session)")
	cmd.Flags().BoolVar(&noSession, "no-session", false, "ignore saved session; start a fresh conversation")
	cmd.Flags().BoolVar(&compareOV, "compare-openviking", false, "run the same prompt twice (OpenViking off vs on) and print token/cost deltas")
	return cmd
}

type execCompareSample struct {
	label    string
	result   *runner.RunResult
	usage    tokenUsage
	duration time.Duration
}

func runExecOpenVikingCompare(cmd *cobra.Command, root string, r *runner.Runner, project, agentName, promptText string) error {
	fmt.Fprintf(os.Stderr, "▶  compare %s/%s  (OpenViking off vs on)\n", project, agentName)
	fmt.Fprintf(os.Stderr, "   note   : compare mode always starts fresh runs (no session resume)\n\n")

	samples := make([]execCompareSample, 0, 2)
	cases := []struct {
		label string
		env   map[string]string
	}{
		{
			label: "baseline",
			env:   openVikingCompareEnv(false),
		},
		{
			label: "openviking",
			env:   openVikingCompareEnv(true),
		},
	}

	for _, tc := range cases {
		fmt.Fprintf(os.Stderr, "▶  %s\n\n", tc.label)
		started := time.Now()
		result, err := r.ExecPromptWithEnv(project, agentName, promptText, "", tc.env)
		duration := time.Since(started)
		if err != nil {
			return fmt.Errorf("%s run failed: %w", tc.label, err)
		}
		usage, parseErr := parseLogTokensFromRunResult(root, result)
		if parseErr != nil {
			return fmt.Errorf("%s run parse tokens: %w", tc.label, parseErr)
		}
		samples = append(samples, execCompareSample{
			label:    tc.label,
			result:   result,
			usage:    usage,
			duration: duration,
		})
		fmt.Fprintf(os.Stderr, "\n── %s complete ───────────────────────────────\n", tc.label)
		fmt.Fprintf(os.Stderr, "status  : %s\n", result.Status)
		fmt.Fprintf(os.Stderr, "log     : %s\n", result.LogPath)
		fmt.Fprintf(os.Stderr, "prompt  : %d bytes\n", result.PromptBytes)
		fmt.Fprintf(os.Stderr, "memory  : %d snippet(s)\n", result.MemorySnippets)
		if len(result.MemoryWarnings) > 0 {
			fmt.Fprintf(os.Stderr, "warning : %s\n", strings.Join(result.MemoryWarnings, " | "))
		}
		if result.ErrorMsg != "" {
			fmt.Fprintf(os.Stderr, "error   : %s\n", result.ErrorMsg)
		}
		fmt.Fprintf(os.Stderr, "tokens  : in=%s out=%s cache=%s cost=%s\n",
			formatTokens(usage.InputTokens),
			formatTokens(usage.OutputTokens),
			formatTokens(usage.CacheReadTokens),
			formatUsageCost(usage),
		)
		fmt.Fprintf(os.Stderr, "elapsed : %s\n", duration.Round(time.Millisecond))
		fmt.Fprintf(os.Stderr, "──────────────────────────────────────────────\n\n")
	}

	if len(samples) != 2 {
		return fmt.Errorf("compare mode expected 2 samples, got %d", len(samples))
	}
	base := samples[0]
	ov := samples[1]

	fmt.Fprintf(os.Stdout, "OPENVIKING COMPARE\n")
	fmt.Fprintf(os.Stdout, "project=%s agent=%s\n\n", project, agentName)
	fmt.Fprintf(os.Stdout, "%-12s  %-10s  %-10s  %-10s  %-10s  %-12s  %-8s  %s\n",
		"variant", "input", "output", "cache", "cost", "prompt_bytes", "memory", "elapsed")
	for _, sample := range samples {
		fmt.Fprintf(os.Stdout, "%-12s  %-10s  %-10s  %-10s  %-10s  %-12d  %-8d  %s\n",
			sample.label,
			formatTokens(sample.usage.InputTokens),
			formatTokens(sample.usage.OutputTokens),
			formatTokens(sample.usage.CacheReadTokens),
			formatUsageCost(sample.usage),
			sample.result.PromptBytes,
			sample.result.MemorySnippets,
			sample.duration.Round(time.Millisecond),
		)
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "delta(input)       : %+d\n", ov.usage.InputTokens-base.usage.InputTokens)
	fmt.Fprintf(os.Stdout, "delta(output)      : %+d\n", ov.usage.OutputTokens-base.usage.OutputTokens)
	fmt.Fprintf(os.Stdout, "delta(cache)       : %+d\n", ov.usage.CacheReadTokens-base.usage.CacheReadTokens)
	fmt.Fprintf(os.Stdout, "delta(promptBytes) : %+d\n", ov.result.PromptBytes-base.result.PromptBytes)
	fmt.Fprintf(os.Stdout, "delta(memory)      : %+d\n", ov.result.MemorySnippets-base.result.MemorySnippets)
	if base.usage.HasCost && ov.usage.HasCost {
		fmt.Fprintf(os.Stdout, "delta(costUSD)     : %+0.4f\n", ov.usage.TotalCostUSD-base.usage.TotalCostUSD)
	} else {
		fmt.Fprintf(os.Stdout, "delta(costUSD)     : unavailable\n")
	}
	fmt.Fprintf(os.Stdout, "baseline log       : %s\n", base.result.LogPath)
	fmt.Fprintf(os.Stdout, "openviking log     : %s\n", ov.result.LogPath)
	return nil
}

func parseLogTokensFromRunResult(root string, result *runner.RunResult) (tokenUsage, error) {
	if result == nil || strings.TrimSpace(result.LogPath) == "" {
		return tokenUsage{}, fmt.Errorf("missing run log path")
	}
	return parseLogTokens(root, result.LogPath)
}

func openVikingCompareEnv(enabled bool) map[string]string {
	env := map[string]string{
		"AGENCYCLI_MEMORY_ENABLED": "0",
		// Disable non-OpenViking memory providers so the comparison isolates
		// prompt growth and token spend caused by OpenViking retrieval only.
		"AGENCYCLI_EVERCORE_URL":          "",
		"AGENCYCLI_EVERMEMOS_URL":         "",
		"EVERCORE_URL":                    "",
		"EVERMEMOS_URL":                   "",
		"AGENCYCLI_EVERCORE_API_KEY":      "",
		"AGENCYCLI_EVERMEMOS_API_KEY":     "",
		"EVERCORE_API_KEY":                "",
		"EVERMEMOS_API_KEY":               "",
		"AGENCYCLI_EVERCORE_USER_ID":      "",
		"AGENCYCLI_EVERCORE_GROUP_ID":     "",
		"AGENCYCLI_MEMORY_USER_ID":        "",
		"AGENCYCLI_MEMORY_GROUP_ID":       "",
		"AGENCYCLI_EVERCORE_METHOD":       "",
		"AGENCYCLI_EVERCORE_MEMORY_TYPES": "",
	}
	if enabled {
		env["AGENCYCLI_MEMORY_ENABLED"] = "1"
	}
	return env
}

// resolveExecPrompt returns the prompt string from the first available source:
// --prompt > --file > explicit "-" arg > piped stdin.
func resolveExecPrompt(promptFlag, fileFlag string, args []string) (string, error) {
	if promptFlag != "" {
		return promptFlag, nil
	}
	if fileFlag != "" {
		b, err := os.ReadFile(fileFlag)
		if err != nil {
			return "", fmt.Errorf("read --file: %w", err)
		}
		return string(b), nil
	}
	// Explicit "-" positional arg → read stdin.
	if len(args) == 1 && args[0] == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	// Auto-detect piped stdin (stdin is not a terminal).
	if stat, err := os.Stdin.Stat(); err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	return "", nil
}
