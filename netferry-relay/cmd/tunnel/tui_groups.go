package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// groupsPaneMode tracks the sub-screen of the Groups tab.
type groupsPaneMode int

const (
	groupBrowse groupsPaneMode = iota
	groupRename        // entering a name (for new or rename-existing)
	groupEditChildren  // multi-select children for the active group
	groupConfirmDelete
)

type groupsModel struct {
	root   *rootModel
	mode   groupsPaneMode
	cursor int

	// renaming a specific group (or "" when creating a new group)
	renameID  string
	renameBuf string

	// editing children: target group + cursor + selection set on its profile list
	editingID    string
	childCursor  int
	childChecked map[string]bool
}

func newGroupsModel(root *rootModel) groupsModel {
	return groupsModel{root: root}
}

func (g groupsModel) Init() tea.Cmd { return nil }

func (g *groupsModel) update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch g.mode {
	case groupBrowse:
		return g.updateBrowse(km)
	case groupRename:
		return g.updateRename(km)
	case groupEditChildren:
		return g.updateChildren(km)
	case groupConfirmDelete:
		return g.updateConfirmDelete(km)
	}
	return nil
}

func (g *groupsModel) updateBrowse(km tea.KeyMsg) tea.Cmd {
	groups := g.root.data.groups
	switch km.String() {
	case "up", "k":
		if g.cursor > 0 {
			g.cursor--
		}
	case "down", "j":
		if g.cursor+1 < len(groups) {
			g.cursor++
		}
	case "n":
		// New group — prompt for a name; ID is generated on commit.
		g.renameID = ""
		g.renameBuf = ""
		g.mode = groupRename
	case "r":
		if len(groups) == 0 {
			return nil
		}
		g.renameID = groups[g.cursor].ID
		g.renameBuf = groups[g.cursor].Name
		g.mode = groupRename
	case "enter", "e":
		if len(groups) == 0 {
			return nil
		}
		g.editingID = groups[g.cursor].ID
		g.childCursor = 0
		g.childChecked = map[string]bool{}
		for _, id := range groups[g.cursor].ChildrenIDs {
			g.childChecked[id] = true
		}
		g.mode = groupEditChildren
	case "d":
		if len(groups) == 0 {
			return nil
		}
		g.renameID = groups[g.cursor].ID
		g.mode = groupConfirmDelete
	case "a":
		// Set this group as the active one (mirrors Settings.ActiveGroupID).
		if len(groups) == 0 {
			return nil
		}
		s := g.root.data.settings
		s.ActiveGroupID = groups[g.cursor].ID
		if err := store.SaveSettings(s); err != nil {
			return g.root.setFlash(false, "save: "+err.Error())
		}
		g.root.data.settings = s
		return g.root.setFlash(true, "active group → "+groups[g.cursor].Name)
	}
	return nil
}

func (g *groupsModel) updateRename(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		g.mode = groupBrowse
		return nil
	case "enter":
		name := strings.TrimSpace(g.renameBuf)
		if name == "" {
			return g.root.setFlash(false, "name required")
		}
		var grp *store.Group
		if g.renameID == "" {
			id, err := genID()
			if err != nil {
				return g.root.setFlash(false, err.Error())
			}
			grp = &store.Group{ID: id, Name: name}
		} else {
			existing := findGroupByID(g.root.data.groups, g.renameID)
			if existing == nil {
				return g.root.setFlash(false, "group missing")
			}
			cp := *existing
			cp.Name = name
			grp = &cp
		}
		if err := store.SaveGroup(grp); err != nil {
			return g.root.setFlash(false, "save: "+err.Error())
		}
		g.root.reload()
		g.mode = groupBrowse
		// Move cursor to the saved group so the next action targets it.
		for i, gg := range g.root.data.groups {
			if gg.ID == grp.ID {
				g.cursor = i
				break
			}
		}
		return g.root.setFlash(true, "saved")
	case "backspace":
		if len(g.renameBuf) > 0 {
			g.renameBuf = g.renameBuf[:len(g.renameBuf)-1]
		}
	default:
		s := km.String()
		if len(s) == 1 && s[0] >= 0x20 && s[0] < 0x7f {
			g.renameBuf += s
		}
	}
	return nil
}

func (g *groupsModel) updateChildren(km tea.KeyMsg) tea.Cmd {
	profiles := g.root.data.profiles
	switch km.String() {
	case "esc":
		g.mode = groupBrowse
		return nil
	case "up", "k":
		if g.childCursor > 0 {
			g.childCursor--
		}
	case "down", "j":
		if g.childCursor+1 < len(profiles) {
			g.childCursor++
		}
	case " ", "x":
		if len(profiles) == 0 {
			return nil
		}
		id := profiles[g.childCursor].ID
		g.childChecked[id] = !g.childChecked[id]
	case "ctrl+s", "enter":
		// Save: rebuild ChildrenIDs in the order of the profiles list.
		grp := findGroupByID(g.root.data.groups, g.editingID)
		if grp == nil {
			return g.root.setFlash(false, "group missing")
		}
		updated := *grp
		updated.ChildrenIDs = updated.ChildrenIDs[:0]
		for _, p := range profiles {
			if g.childChecked[p.ID] {
				updated.ChildrenIDs = append(updated.ChildrenIDs, p.ID)
			}
		}
		if err := store.SaveGroup(&updated); err != nil {
			return g.root.setFlash(false, "save: "+err.Error())
		}
		g.root.reload()
		g.mode = groupBrowse
		return g.root.setFlash(true, fmt.Sprintf("saved %d child profile(s)", len(updated.ChildrenIDs)))
	}
	return nil
}

func (g *groupsModel) updateConfirmDelete(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "y", "Y":
		id := g.renameID
		if err := store.DeleteGroup(id); err != nil {
			g.mode = groupBrowse
			return g.root.setFlash(false, "delete: "+err.Error())
		}
		// If the deleted group was active, clear the setting so Destinations
		// falls back to routes.json.
		if g.root.data.settings.ActiveGroupID == id {
			s := g.root.data.settings
			s.ActiveGroupID = ""
			_ = store.SaveSettings(s)
		}
		g.root.reload()
		if g.cursor >= len(g.root.data.groups) {
			g.cursor = max(0, len(g.root.data.groups)-1)
		}
		g.mode = groupBrowse
		return g.root.setFlash(true, "group deleted")
	case "n", "N", "esc":
		g.mode = groupBrowse
	}
	return nil
}

// ── view ─────────────────────────────────────────────────────────────────────

func (g *groupsModel) view(width, height int) string {
	switch g.mode {
	case groupRename:
		return g.viewRename(width, height)
	case groupEditChildren:
		return g.viewChildren(width, height)
	case groupConfirmDelete:
		return g.viewConfirmDelete()
	default:
		return g.viewBrowse(width, height)
	}
}

func (g *groupsModel) viewBrowse(width, height int) string {
	var b strings.Builder
	groups := g.root.data.groups
	active := g.root.data.settings.ActiveGroupID

	if len(groups) == 0 {
		b.WriteString(dimText.Render("(no groups — press [n] to create one)"))
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(dimText.Render("[n] new"))
		return b.String()
	}

	listHeight := height - 2
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := windowedRange(len(groups), g.cursor, listHeight)
	if start > 0 {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↑ %d more above", start)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		gr := groups[i]
		marker := "  "
		if gr.ID == active {
			marker = "★ "
		}
		row := fmt.Sprintf("%s%-25s  %s", marker, gr.Name,
			dimText.Render(fmt.Sprintf("%d profiles · %d rules", len(gr.ChildrenIDs), len(gr.Rules))))
		if i == g.cursor {
			row = listSelected.Render(row)
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	if end < len(groups) {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↓ %d more below", len(groups)-end)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [Enter/e] edit children  [n] new  [r] rename  [a] activate  [d] delete   ★ = active"))
	return b.String()
}

func (g *groupsModel) viewRename(width, height int) string {
	var b strings.Builder
	title := "New group"
	if g.renameID != "" {
		title = "Rename group"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
	b.WriteByte('\n')
	b.WriteByte('\n')
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	b.WriteString("Name: ")
	b.WriteString(listSelected.Render(g.renameBuf) + cursor)
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[Enter] save  [Esc] cancel"))
	return b.String()
}

func (g *groupsModel) viewChildren(width, height int) string {
	var b strings.Builder
	grp := findGroupByID(g.root.data.groups, g.editingID)
	title := "Edit children"
	if grp != nil {
		title = "Children of: " + grp.Name
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
	b.WriteByte('\n')

	profiles := g.root.data.profiles
	if len(profiles) == 0 {
		b.WriteByte('\n')
		b.WriteString(dimText.Render("(no profiles available — create one in the Profiles tab first)"))
		b.WriteByte('\n')
		b.WriteString(dimText.Render("[Esc] back"))
		return b.String()
	}
	listHeight := height - 4
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := windowedRange(len(profiles), g.childCursor, listHeight)
	if start > 0 {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↑ %d more above", start)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		pr := profiles[i]
		mark := "[ ]"
		if g.childChecked[pr.ID] {
			mark = "[x]"
		}
		row := fmt.Sprintf("%s %-30s  %s", mark, pr.Name, dimText.Render(pr.Remote))
		if i == g.childCursor {
			row = "▶ " + listSelected.Render(row)
		} else {
			row = "  " + row
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	if end < len(profiles) {
		b.WriteString(dimText.Render(fmt.Sprintf("  ↓ %d more below", len(profiles)-end)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(dimText.Render("[↑/↓] move  [Space/x] toggle  [Enter/Ctrl+S] save  [Esc] cancel"))
	return b.String()
}

func (g *groupsModel) viewConfirmDelete() string {
	name := g.renameID
	if grp := findGroupByID(g.root.data.groups, g.renameID); grp != nil {
		name = grp.Name
	}
	return fmt.Sprintf("Delete group %q? Child profiles are NOT deleted. [y/N]", name)
}
