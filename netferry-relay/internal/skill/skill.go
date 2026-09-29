// Package skill embeds the netferry-tunnel Claude Code skill so the CLI can
// install it into ~/.claude/skills/netferry-tunnel/SKILL.md and auto-update
// it on version bumps.
package skill

import _ "embed"

//go:embed skill.md
var Embedded string
