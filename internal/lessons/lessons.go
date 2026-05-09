// Package lessons captures reusable task learnings and turns them into skill
// drafts that can later be promoted into the workspace skills directory.
package lessons

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	BeginMarker = "AGENCYCLI_LESSON_BEGIN"
	EndMarker   = "AGENCYCLI_LESSON_END"

	lessonsDir = "lessons"
	draftsDir  = "skill-drafts"
	skillsDir  = "skills"
)

// CaptureInput describes the task run that produced a lesson block.
type CaptureInput struct {
	Project   string
	Agent     string
	TaskID    string
	TaskTitle string
	Status    string
	LogPath   string
	CreatedAt time.Time
}

// Metadata is stored as YAML frontmatter at the top of every lesson file.
type Metadata struct {
	ID        string    `yaml:"id"`
	Project   string    `yaml:"project"`
	Agent     string    `yaml:"agent"`
	TaskID    string    `yaml:"task_id"`
	TaskTitle string    `yaml:"task_title,omitempty"`
	Status    string    `yaml:"status,omitempty"`
	LogPath   string    `yaml:"log_path,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
}

// Lesson is a captured reusable learning with its source path.
type Lesson struct {
	Metadata Metadata
	Body     string
	Path     string
}

// CaptureFromOutput extracts all explicit lesson blocks from output and writes
// them under the owning agent's .agencycli/lessons directory.
func CaptureFromOutput(root string, input CaptureInput, output string) ([]string, error) {
	blocks := ExtractBlocks(output)
	if len(blocks) == 0 {
		return nil, nil
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}

	dir := AgentLessonsDir(root, input.Project, input.Agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(blocks))
	for i, block := range blocks {
		title := firstTitle(block, input.TaskTitle)
		id := lessonID(input.TaskID, title, i+1)
		meta := Metadata{
			ID:        id,
			Project:   input.Project,
			Agent:     input.Agent,
			TaskID:    input.TaskID,
			TaskTitle: input.TaskTitle,
			Status:    input.Status,
			LogPath:   input.LogPath,
			CreatedAt: input.CreatedAt.UTC(),
		}
		content, err := renderLesson(meta, block)
		if err != nil {
			return paths, err
		}
		name := fmt.Sprintf("%s-%s.md", input.CreatedAt.UTC().Format("20060102-150405"), id)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// ExtractBlocks returns trimmed bodies found between the explicit lesson
// markers in a model's output.
func ExtractBlocks(output string) []string {
	var blocks []string
	rest := output
	for {
		start := strings.Index(rest, BeginMarker)
		if start == -1 {
			break
		}
		rest = rest[start+len(BeginMarker):]
		end := strings.Index(rest, EndMarker)
		if end == -1 {
			break
		}
		block := strings.TrimSpace(rest[:end])
		if block != "" {
			blocks = append(blocks, block)
		}
		rest = rest[end+len(EndMarker):]
	}
	return blocks
}

// AgentLessonsDir returns the directory where an agent's captured lessons live.
func AgentLessonsDir(root, project, agent string) string {
	return filepath.Join(root, "projects", project, "agents", agent, ".agencycli", lessonsDir)
}

// List returns captured lessons for one agent. Lessons are sorted oldest first.
func List(root, project, agent string) ([]Lesson, error) {
	dir := AgentLessonsDir(root, project, agent)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	out := make([]Lesson, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		lesson, err := Read(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		out = append(out, lesson)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Metadata.CreatedAt.Equal(b.Metadata.CreatedAt) {
			return a.Metadata.CreatedAt.Before(b.Metadata.CreatedAt)
		}
		return a.Path < b.Path
	})
	return out, nil
}

// Read parses one captured lesson file.
func Read(path string) (Lesson, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Lesson{}, err
	}
	meta, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return Lesson{}, err
	}
	return Lesson{Metadata: meta, Body: strings.TrimSpace(body), Path: path}, nil
}

// Select chooses lessons by selector: "latest", "all", or a comma-separated
// list of lesson IDs, task IDs, filenames, or filename stems.
func Select(root, project, agent, selector string) ([]Lesson, error) {
	lessons, err := List(root, project, agent)
	if err != nil {
		return nil, err
	}
	if len(lessons) == 0 {
		return nil, fmt.Errorf("no lessons found for %s/%s", project, agent)
	}
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == "latest" {
		return []Lesson{lessons[len(lessons)-1]}, nil
	}
	if selector == "all" {
		return lessons, nil
	}

	want := map[string]bool{}
	for _, part := range strings.Split(selector, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			want[part] = true
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("empty lesson selector")
	}

	var selected []Lesson
	seen := map[string]bool{}
	for _, lesson := range lessons {
		base := filepath.Base(lesson.Path)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		keys := []string{
			lesson.Metadata.ID,
			lesson.Metadata.TaskID,
			base,
			stem,
		}
		for _, key := range keys {
			if want[key] && !seen[lesson.Path] {
				selected = append(selected, lesson)
				seen[lesson.Path] = true
				break
			}
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no lessons matched selector %q", selector)
	}
	return selected, nil
}

// RenderDraft converts selected lessons into a concise SKILL.md body.
func RenderDraft(name, description string, selected []Lesson) (string, error) {
	name = NormalizeName(name)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("at least one lesson is required")
	}
	if strings.TrimSpace(description) == "" {
		description = defaultDescription(name, selected)
	}

	meta, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{
		Name:        name,
		Description: strings.TrimSpace(description),
	})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(meta)
	sb.WriteString("---\n\n")
	sb.WriteString("# ")
	sb.WriteString(titleFromName(name))
	sb.WriteString("\n\n")
	sb.WriteString("Use this skill when the task matches the captured lessons below. Apply only the reusable parts, and verify with the checks listed in the source lessons.\n\n")
	sb.WriteString("## Workflow\n\n")
	sb.WriteString("1. Identify which captured lesson matches the current task.\n")
	sb.WriteString("2. Follow its concrete steps and adapt names, paths, and commands to the current project.\n")
	sb.WriteString("3. Re-run the listed validation checks before marking the task complete.\n")
	sb.WriteString("4. If the lesson no longer applies, record a new lesson instead of forcing this one.\n\n")
	sb.WriteString("## Captured Lessons\n\n")

	for _, lesson := range selected {
		title := firstTitle(lesson.Body, lesson.Metadata.TaskTitle)
		sb.WriteString("### ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
		if lesson.Metadata.Project != "" || lesson.Metadata.Agent != "" || lesson.Metadata.TaskID != "" {
			sb.WriteString("Source: ")
			sb.WriteString(strings.Trim(lesson.Metadata.Project+"/"+lesson.Metadata.Agent, "/"))
			if lesson.Metadata.TaskID != "" {
				sb.WriteString(" task ")
				sb.WriteString(lesson.Metadata.TaskID)
			}
			sb.WriteString("\n\n")
		}
		sb.WriteString(strings.TrimSpace(lesson.Body))
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// WriteDraft writes a generated skill draft under skill-drafts/<name>/SKILL.md.
func WriteDraft(root, name, content string, force bool) (string, error) {
	name = NormalizeName(name)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	path := filepath.Join(root, draftsDir, name, "SKILL.md")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("draft %q already exists; pass --force to overwrite", name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(content), 0o644)
}

// PromoteDraft copies skill-drafts/<name>/SKILL.md into skills/<name>/SKILL.md.
func PromoteDraft(root, name string, force bool) (string, error) {
	name = NormalizeName(name)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	src := filepath.Join(root, draftsDir, name, "SKILL.md")
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read draft %q: %w", name, err)
	}
	dst := filepath.Join(root, skillsDir, name, "SKILL.md")
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return "", fmt.Errorf("skill %q already exists; pass --force to overwrite", name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	return dst, os.WriteFile(dst, data, 0o644)
}

// DraftPath returns the expected SKILL.md path for a draft name.
func DraftPath(root, name string) string {
	return filepath.Join(root, draftsDir, NormalizeName(name), "SKILL.md")
}

// NormalizeName converts arbitrary text into a stable skill directory name.
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var sb strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			sb.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && sb.Len() > 0 {
				sb.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(sb.String(), "-_.")
}

func renderLesson(meta Metadata, body string) (string, error) {
	data, err := yaml.Marshal(meta)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(data)
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(body))
	sb.WriteString("\n")
	return sb.String(), nil
}

func splitFrontmatter(content string) (Metadata, string, error) {
	var meta Metadata
	if !strings.HasPrefix(content, "---") {
		return meta, content, nil
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return meta, content, nil
	}
	if err := yaml.Unmarshal([]byte(rest[:idx]), &meta); err != nil {
		return meta, "", err
	}
	body := strings.TrimPrefix(rest[idx+4:], "\n")
	return meta, body, nil
}

func lessonID(taskID, title string, index int) string {
	parts := []string{}
	if taskID != "" {
		parts = append(parts, NormalizeName(taskID))
	}
	if title != "" {
		parts = append(parts, NormalizeName(title))
	}
	if len(parts) == 0 {
		parts = append(parts, "lesson")
	}
	id := strings.Join(parts, "-")
	if index > 1 {
		id = fmt.Sprintf("%s-%02d", id, index)
	}
	if len(id) > 96 {
		id = strings.Trim(id[:96], "-_.")
	}
	return id
}

func firstTitle(body, fallback string) string {
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
			if len(line) > 90 {
				return strings.TrimSpace(line[:90])
			}
			return line
		}
	}
	if fallback != "" {
		return fallback
	}
	return "Reusable lesson"
}

func defaultDescription(name string, selected []Lesson) string {
	title := titleFromName(name)
	if len(selected) == 1 {
		source := selected[0].Metadata.TaskTitle
		if source == "" {
			source = firstTitle(selected[0].Body, "")
		}
		return fmt.Sprintf("Use when applying the reusable workflow learned from: %s.", source)
	}
	return fmt.Sprintf("Use when applying reusable %s workflows learned from prior agent tasks.", title)
}

func titleFromName(name string) string {
	name = strings.ReplaceAll(NormalizeName(name), "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, ".", " ")
	words := strings.Fields(name)
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	if len(words) == 0 {
		return "Generated Skill"
	}
	return strings.Join(words, " ")
}
