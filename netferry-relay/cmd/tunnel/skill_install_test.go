package main

import (
	"strings"
	"testing"

	"github.com/hoveychen/netferry/relay/internal/skill"
)

func TestParseSkillVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"---\nname: x\nversion: 3\n---\nbody", 3},
		{"---\nname: x\n---\nversion: 9\n", 0},
		{"no front matter", 0},
	}
	for _, c := range cases {
		if got := parseSkillVersion(c.in); got != c.want {
			t.Errorf("parseSkillVersion(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	if parseSkillVersion(skill.Embedded) < 1 {
		t.Fatal("embedded skill has no version")
	}
}

func TestRenderSkillFillsBinary(t *testing.T) {
	out := renderSkill()
	if strings.Contains(out, "{{BIN}}") {
		t.Fatal("placeholder left in rendered skill")
	}
	if parseSkillVersion(out) != parseSkillVersion(skill.Embedded) {
		t.Fatal("rendering changed the version")
	}
}
