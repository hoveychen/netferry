package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldArea
	fieldToggle
	fieldSelect
	fieldButton
)

// Option is one choice of a select field.
type Option struct {
	Label string
	Value string
}

// Field is one row of a Form. Build with the textField/toggleField/... helpers.
type Field struct {
	Key     string
	Label   string
	Help    string
	Section string      // rendered as a header above this field when set
	Visible func() bool // nil = always visible
	kind    fieldKind
	input   textinput.Model
	area    textarea.Model
	on      bool
	options []Option
	sel     int
	action  func() tea.Cmd // button
}

func textField(key, label, value, placeholder string) *Field {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	in.SetValue(value)
	in.CharLimit = 4096
	return &Field{Key: key, Label: label, kind: fieldText, input: in}
}

func areaField(key, label, value string, height int) *Field {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.CharLimit = 0
	ta.SetHeight(height)
	ta.SetValue(value)
	return &Field{Key: key, Label: label, kind: fieldArea, area: ta}
}

func toggleField(key, label string, on bool) *Field {
	return &Field{Key: key, Label: label, kind: fieldToggle, on: on}
}

func selectField(key, label string, options []Option, value string) *Field {
	f := &Field{Key: key, Label: label, kind: fieldSelect, options: options}
	f.SetValue(value)
	return f
}

func buttonField(key, label string, action func() tea.Cmd) *Field {
	return &Field{Key: key, Label: label, kind: fieldButton, action: action}
}

func (f *Field) visible() bool { return f.Visible == nil || f.Visible() }

// Text returns the trimmed text of a text/area field.
func (f *Field) Text() string {
	switch f.kind {
	case fieldText:
		return strings.TrimSpace(f.input.Value())
	case fieldArea:
		return strings.TrimSpace(f.area.Value())
	}
	return ""
}

// Raw returns the untrimmed text of a text/area field.
func (f *Field) Raw() string {
	if f.kind == fieldArea {
		return f.area.Value()
	}
	return f.input.Value()
}

func (f *Field) SetText(v string) {
	if f.kind == fieldArea {
		f.area.SetValue(v)
	} else {
		f.input.SetValue(v)
	}
}

func (f *Field) On() bool      { return f.on }
func (f *Field) SetOn(on bool) { f.on = on }
func (f *Field) Value() string {
	if f.sel >= 0 && f.sel < len(f.options) {
		return f.options[f.sel].Value
	}
	return ""
}

func (f *Field) SetValue(v string) {
	f.sel = 0
	for i, o := range f.options {
		if o.Value == v {
			f.sel = i
			return
		}
	}
}

func (f *Field) SetOptions(opts []Option) {
	cur := f.Value()
	f.options = opts
	f.SetValue(cur)
}

// Form is a vertical list of fields with one focused at a time.
type Form struct {
	Fields   []*Field
	Errors   map[string]string
	focus    int
	OnChange func(key string) // called after a toggle/select/text edit
}

func newForm(fields ...*Field) *Form {
	f := &Form{Fields: fields, Errors: map[string]string{}}
	f.focus = -1
	f.move(1)
	return f
}

// Field looks up a field by key (nil if absent).
func (f *Form) Field(key string) *Field {
	for _, fl := range f.Fields {
		if fl.Key == key {
			return fl
		}
	}
	return nil
}

func (f *Form) focused() *Field {
	if f.focus >= 0 && f.focus < len(f.Fields) {
		return f.Fields[f.focus]
	}
	return nil
}

// Capturing reports whether keystrokes go to a text input, so the page must
// not treat letters as shortcuts.
func (f *Form) Capturing() bool {
	fl := f.focused()
	return fl != nil && (fl.kind == fieldText || fl.kind == fieldArea)
}

func (f *Form) blurAll() {
	for _, fl := range f.Fields {
		fl.input.Blur()
		fl.area.Blur()
	}
}

// move focuses the next/previous visible field.
func (f *Form) move(dir int) {
	n := len(f.Fields)
	if n == 0 {
		return
	}
	i := f.focus
	for step := 0; step < n; step++ {
		i = (i + dir + n) % n
		if f.Fields[i].visible() {
			break
		}
	}
	f.focus = i
	f.blurAll()
	if fl := f.focused(); fl != nil {
		switch fl.kind {
		case fieldText:
			fl.input.Focus()
		case fieldArea:
			fl.area.Focus()
		}
	}
}

// FocusKey moves focus to the field with key.
func (f *Form) FocusKey(key string) {
	for i, fl := range f.Fields {
		if fl.Key == key {
			f.focus = i - 1
			f.move(1)
			if f.focus != i {
				f.focus = i
			}
			return
		}
	}
}

func (f *Form) changed(key string) {
	if f.OnChange != nil {
		f.OnChange(key)
	}
	// A change may hide the focused field.
	if fl := f.focused(); fl != nil && !fl.visible() {
		f.move(1)
	}
}

// Update routes a key to the focused field. Returns handled=false for keys
// the form does not use (so the page can treat them, e.g. ctrl+s / esc).
func (f *Form) Update(msg tea.Msg) (tea.Cmd, bool) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	fl := f.focused()
	switch km.String() {
	case "tab", "down", "ctrl+n":
		if fl != nil && fl.kind == fieldArea && km.String() == "down" {
			break // let the textarea move its cursor
		}
		f.move(1)
		return nil, true
	case "shift+tab", "up", "ctrl+p":
		if fl != nil && fl.kind == fieldArea && km.String() == "up" {
			break
		}
		f.move(-1)
		return nil, true
	case "esc", "ctrl+s":
		return nil, false
	}
	if fl == nil {
		return nil, false
	}
	switch fl.kind {
	case fieldToggle:
		switch km.String() {
		case " ", "enter", "left", "right", "h", "l":
			fl.on = !fl.on
			f.changed(fl.Key)
			return nil, true
		}
	case fieldSelect:
		switch km.String() {
		case "right", "l", " ", "enter":
			if len(fl.options) > 0 {
				fl.sel = (fl.sel + 1) % len(fl.options)
				f.changed(fl.Key)
			}
			return nil, true
		case "left", "h":
			if len(fl.options) > 0 {
				fl.sel = (fl.sel - 1 + len(fl.options)) % len(fl.options)
				f.changed(fl.Key)
			}
			return nil, true
		}
	case fieldButton:
		if km.String() == "enter" || km.String() == " " {
			if fl.action != nil {
				return fl.action(), true
			}
			return nil, true
		}
	case fieldText:
		if km.String() == "enter" {
			f.move(1)
			return nil, true
		}
		var cmd tea.Cmd
		before := fl.input.Value()
		fl.input, cmd = fl.input.Update(msg)
		if fl.input.Value() != before {
			f.changed(fl.Key)
		}
		return cmd, true
	case fieldArea:
		var cmd tea.Cmd
		before := fl.area.Value()
		fl.area, cmd = fl.area.Update(msg)
		if fl.area.Value() != before {
			f.changed(fl.Key)
		}
		return cmd, true
	}
	return nil, false
}

const formLabelWidth = 22

// View renders the visible fields; width is the available cells.
func (f *Form) View(width int) string {
	var b strings.Builder
	valueW := width - formLabelWidth - 3
	if valueW < 10 {
		valueW = 10
	}
	for i, fl := range f.Fields {
		if !fl.visible() {
			continue
		}
		if fl.Section != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(sSection.Render(strings.ToUpper(fl.Section)))
			b.WriteByte('\n')
		}
		focused := i == f.focus
		marker := "  "
		label := sMuted.Render(padRight(truncate(fl.Label, formLabelWidth), formLabelWidth))
		if focused {
			marker = sAccent.Render("▸ ")
			label = sBold.Render(padRight(truncate(fl.Label, formLabelWidth), formLabelWidth))
		}
		var value string
		switch fl.kind {
		case fieldText:
			fl.input.Width = valueW
			value = fl.input.View()
		case fieldArea:
			fl.area.SetWidth(valueW)
			value = fl.area.View()
		case fieldToggle:
			if fl.on {
				value = sOK.Render("[x] on")
			} else {
				value = sMuted.Render("[ ] off")
			}
		case fieldSelect:
			cur := ""
			if fl.sel < len(fl.options) {
				cur = fl.options[fl.sel].Label
			}
			value = "‹ " + cur + " ›"
			if focused {
				value = sAccent.Render(value)
			}
		case fieldButton:
			value = lipgloss.NewStyle().Foreground(cAccent).Render("[ " + fl.Label + " ]")
			label = padRight("", formLabelWidth)
		}
		if fl.kind == fieldArea {
			b.WriteString(marker + label + "\n")
			for _, line := range strings.Split(value, "\n") {
				b.WriteString("    " + line + "\n")
			}
		} else {
			b.WriteString(marker + label + " " + value + "\n")
		}
		if msg, ok := f.Errors[fl.Key]; ok && msg != "" {
			b.WriteString(padRight("", formLabelWidth+3) + sErr.Render("✗ "+msg) + "\n")
		} else if focused && fl.Help != "" {
			b.WriteString(padRight("", formLabelWidth+3) + sMuted.Render(truncate(fl.Help, valueW)) + "\n")
		}
	}
	return b.String()
}

// scrollTo returns the window of lines [top, top+h) that keeps the focused
// row visible. Pages call it with the rendered form to scroll long forms.
func (f *Form) scrollWindow(rendered string, h int) string {
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if h <= 0 || len(lines) <= h {
		return strings.Join(lines, "\n")
	}
	focusLine := 0
	for i, l := range lines {
		if strings.Contains(l, "▸ ") {
			focusLine = i
			break
		}
	}
	top := focusLine - h/2
	if top < 0 {
		top = 0
	}
	if top > len(lines)-h {
		top = len(lines) - h
	}
	return strings.Join(lines[top:top+h], "\n")
}
