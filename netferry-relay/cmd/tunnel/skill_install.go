package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/skill"
)

const skillToolName = "netferry-tunnel"

func claudeSkillPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills", skillToolName, "SKILL.md"), nil
}

// renderSkill fills in the path of this binary, since netferry-tunnel is
// usually not on PATH (the desktop app bundles it).
func renderSkill() string {
	bin := skillToolName
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		bin = exe
	}
	return strings.ReplaceAll(skill.Embedded, "{{BIN}}", bin)
}

// parseSkillVersion returns the integer `version:` of the skill front matter,
// or 0 if it is missing.
func parseSkillVersion(content string) int {
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return 0
	}
	for _, l := range strings.Split(parts[1], "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "version:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// checkSkillNotice runs before `conns`: it suggests installing the skill when
// it is missing, updates it when the embedded version is newer, and quietly
// refreshes the recorded binary path when only that changed.
func checkSkillNotice() {
	path, err := claudeSkillPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tip: run `%s install-claude-skill` to add Claude Code skill support.\n", skillToolName)
		return
	}
	installed, embedded := parseSkillVersion(string(data)), parseSkillVersion(skill.Embedded)
	switch {
	case installed < embedded:
		fmt.Fprintln(os.Stderr, "Notice: Claude skill is outdated. Auto-updating...")
		if err := writeSkill(path); err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %v. Run `%s install-claude-skill` to update manually.\n", err, skillToolName)
		}
	case installed == embedded && string(data) != renderSkill():
		writeSkill(path)
	}
}

func writeSkill(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderSkill()), 0o644); err != nil {
		return fmt.Errorf("writing skill file: %w", err)
	}
	return nil
}

// runInstallClaudeSkill implements `netferry-tunnel install-claude-skill`.
func runInstallClaudeSkill(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
		fmt.Fprintf(os.Stderr, "usage: %s install-claude-skill\n\nInstall or update the Claude Code skill into ~/.claude/skills/%s/SKILL.md.\n", skillToolName, skillToolName)
		return
	}
	path, err := claudeSkillPath()
	if err != nil {
		fatalf("install-claude-skill: %v", err)
	}
	if err := writeSkill(path); err != nil {
		fatalf("install-claude-skill: %v", err)
	}
	fmt.Printf("Claude Code skill installed at %s\n", path)
}
