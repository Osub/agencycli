package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/chenhg5/agencycli/internal/lessons"
	"github.com/chenhg5/agencycli/internal/store"
	"github.com/spf13/cobra"
)

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage learned skills and captured lessons",
	}
	cmd.AddCommand(
		newSkillLessonsCmd(),
		newSkillDraftCmd(),
		newSkillPromoteCmd(),
	)
	return cmd
}

func newSkillLessonsCmd() *cobra.Command {
	var project, agentName string

	cmd := &cobra.Command{
		Use:     "lessons",
		Aliases: []string{"lesson"},
		Short:   "List reusable lessons captured from an agent's task output",
		Example: `  agencycli skill lessons --project my-api --agent dev
  agencycli skill lesson --project my-api --agent qa`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" || agentName == "" {
				return fmt.Errorf("--project and --agent are required")
			}
			root, err := resolveRoot()
			if err != nil {
				return err
			}
			items, err := lessons.List(root, project, agentName)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Printf("No lessons found for %s/%s.\n", project, agentName)
				return nil
			}
			fmt.Printf("Lessons for %s/%s:\n", project, agentName)
			for _, item := range items {
				meta := item.Metadata
				title := firstMarkdownTitle(item.Body, meta.TaskTitle)
				fmt.Printf("  %-36s  task:%-20s  %s\n", meta.ID, meta.TaskID, title)
				fmt.Printf("      %s\n", item.Path)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name")
	return cmd
}

func newSkillDraftCmd() *cobra.Command {
	var (
		project     string
		agentName   string
		selector    string
		name        string
		description string
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Generate a skill draft from captured lessons",
		Example: `  agencycli skill draft --project my-api --agent dev --lesson latest --name go-test-retry
  agencycli skill draft --project my-api --agent qa --lesson all --name qa-regression-checks --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" || agentName == "" || name == "" {
				return fmt.Errorf("--project, --agent and --name are required")
			}
			root, err := resolveRoot()
			if err != nil {
				return err
			}
			selected, err := lessons.Select(root, project, agentName, selector)
			if err != nil {
				return err
			}
			content, err := lessons.RenderDraft(name, description, selected)
			if err != nil {
				return err
			}
			path, err := lessons.WriteDraft(root, name, content, force)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Skill draft written: %s\n", path)
			fmt.Printf("  Lessons used: %d\n", len(selected))
			fmt.Printf("  Promote with: agencycli skill promote --name %s\n", lessons.NormalizeName(name))
			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name")
	cmd.Flags().StringVar(&selector, "lesson", "latest", "lesson selector: latest, all, or comma-separated IDs/task IDs/filenames")
	cmd.Flags().StringVar(&name, "name", "", "skill name")
	cmd.Flags().StringVar(&description, "description", "", "skill frontmatter description")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing draft")
	return cmd
}

func newSkillPromoteCmd() *cobra.Command {
	var (
		name      string
		teamPath  string
		roleName  string
		force     bool
		sync      bool
		project   string
		agentName string
	)

	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote a skill draft into the shared skills directory",
		Example: `  agencycli skill promote --name go-test-retry
  agencycli skill promote --name qa-regression-checks --team engineering --role qa --sync --project my-api
  agencycli skill promote --name release-checklist --team engineering --sync --project my-api --agent dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if roleName != "" && teamPath == "" {
				return fmt.Errorf("--team is required when --role is set")
			}
			if agentName != "" && project == "" {
				return fmt.Errorf("--project is required when --agent is set")
			}

			root, err := resolveRoot()
			if err != nil {
				return err
			}
			s := store.NewFS(root)
			skillName := lessons.NormalizeName(name)

			path, err := lessons.PromoteDraft(root, skillName, force)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Skill promoted: %s\n", path)

			if teamPath != "" {
				if roleName != "" {
					if err := bindSkillToRole(s, teamPath, roleName, skillName); err != nil {
						return err
					}
					fmt.Printf("✓ Bound skill %q to role %q/%q\n", skillName, teamPath, roleName)
				} else {
					if err := bindSkillToTeam(s, teamPath, skillName); err != nil {
						return err
					}
					fmt.Printf("✓ Bound skill %q to team %q\n", skillName, teamPath)
				}
			}

			if sync {
				if err := syncPromotedSkill(root, s, project, agentName); err != nil {
					return err
				}
			} else {
				fmt.Println("  Run `agencycli sync` to push the updated context to hired agents.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "skill draft name")
	cmd.Flags().StringVar(&teamPath, "team", "", "team to bind the promoted skill to")
	cmd.Flags().StringVar(&roleName, "role", "", "role to bind the promoted skill to; requires --team")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing promoted skill")
	cmd.Flags().BoolVar(&sync, "sync", false, "run sync after promotion")
	cmd.Flags().StringVar(&project, "project", "", "limit sync to this project")
	cmd.Flags().StringVar(&agentName, "agent", "", "limit sync to this agent; requires --project")
	return cmd
}

func bindSkillToTeam(s store.Store, teamPath, skillName string) error {
	team, err := s.Team(teamPath)
	if err != nil {
		return err
	}
	for _, existing := range team.Skills {
		if existing == skillName {
			return nil
		}
	}
	team.Skills = append(team.Skills, skillName)
	return s.SaveTeam(teamPath, team)
}

func bindSkillToRole(s store.Store, teamPath, roleName, skillName string) error {
	role, err := s.Role(teamPath, roleName)
	if err != nil {
		return err
	}
	for _, existing := range role.Skills {
		if existing == skillName {
			return nil
		}
	}
	role.Skills = append(role.Skills, skillName)
	return s.SaveRole(teamPath, roleName, role)
}

func syncPromotedSkill(root string, s store.Store, project, agentName string) error {
	type target struct{ project, name string }
	var targets []target

	if project != "" && agentName != "" {
		targets = append(targets, target{project, agentName})
	} else if project != "" {
		agents, err := s.ListAgents(project)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			targets = append(targets, target{project, agent.Name})
		}
	} else {
		projects, err := s.ListProjects()
		if err != nil {
			return err
		}
		for _, p := range projects {
			agents, err := s.ListAgents(p.Name)
			if err != nil {
				continue
			}
			for _, agent := range agents {
				targets = append(targets, target{p.Name, agent.Name})
			}
		}
	}

	if len(targets) == 0 {
		fmt.Println("No agents found to sync.")
		return nil
	}
	for _, t := range targets {
		if _, err := syncAgent(root, s, t.project, t.name, true); err != nil {
			fmt.Fprintf(os.Stderr, "  sync failed %s/%s: %v\n", t.project, t.name, err)
			continue
		}
		fmt.Printf("  ✓ synced %s/%s\n", t.project, t.name)
	}
	return nil
}

func firstMarkdownTitle(body, fallback string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if title != "" {
				return title
			}
		}
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "-*` "))
		if line != "" {
			return line
		}
	}
	if fallback != "" {
		return fallback
	}
	return "Reusable lesson"
}
