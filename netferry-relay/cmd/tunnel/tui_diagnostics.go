package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hoveychen/netferry/relay/internal/store"
)

// diagnosticsModel runs `nexttrace <target>` and streams stdout into a
// scrolling viewport. Mirrors the desktop Diagnostics page.
type diagnosticsModel struct {
	root      *rootModel
	target    string
	editing   bool
	output    []string
	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	resolved  string // resolved nexttrace binary path, populated on first run
}

func newDiagnosticsModel(root *rootModel) diagnosticsModel {
	return diagnosticsModel{root: root, editing: true}
}

func (d diagnosticsModel) Init() tea.Cmd { return nil }

func (d *diagnosticsModel) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if d.editing {
			return d.updateEditing(m)
		}
		switch m.String() {
		case "i":
			d.editing = true
		case "s":
			if d.running {
				if d.cancel != nil {
					d.cancel()
				}
			}
		case "c":
			d.mu.Lock()
			d.output = nil
			d.mu.Unlock()
		}
	case traceBatchMsg:
		return d.handleTraceBatch(m)
	}
	return nil
}

func (d *diagnosticsModel) updateEditing(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		d.editing = false
	case "enter":
		t := strings.TrimSpace(d.target)
		if t == "" {
			return d.root.setFlash(false, "target required")
		}
		d.editing = false
		return d.startTrace(t)
	case "backspace":
		if len(d.target) > 0 {
			d.target = d.target[:len(d.target)-1]
		}
	default:
		s := msg.String()
		if len(s) == 1 && s[0] >= 0x20 && s[0] < 0x7f {
			d.target += s
		}
	}
	return nil
}

func (d *diagnosticsModel) startTrace(target string) tea.Cmd {
	bin, err := resolveNextTrace()
	if err != nil {
		return d.root.setFlash(false, err.Error())
	}
	d.resolved = bin
	d.output = nil
	d.running = true
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	return func() tea.Msg {
		cmd := exec.CommandContext(ctx, bin, target)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return traceBatchMsg{err: err}
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			return traceBatchMsg{err: err}
		}
		// We need to forward each line as a tea.Msg without blocking the cmd.
		// Trick: spawn a goroutine that writes to a buffered channel, and
		// poll-read it inline so each yielded line becomes a fresh message
		// chained back into the program.
		// Easier: call program.Send via a long-lived hook stored on the model.
		// Simpler still: scan synchronously and accumulate, return once done.
		// We pick the simple path — buffer the full output then return it
		// in chunks via a wrapping batch.
		var lines []string
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		err = cmd.Wait()
		// The scan is synchronous; deliver all collected lines as one batch.
		return traceBatchMsg{lines: lines, err: err}
	}
}

type traceBatchMsg struct {
	lines []string
	err   error
}

func resolveNextTrace() (string, error) {
	if env := strings.TrimSpace(os.Getenv("NETFERRY_NEXTTRACE_BIN")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	dir, err := store.DataDir()
	if err == nil {
		name := "nexttrace"
		if runtime.GOOS == "windows" {
			name = "nexttrace.exe"
		}
		p := filepath.Join(dir, "nexttrace", name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("nexttrace"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("nexttrace not found — install via the desktop app or set NETFERRY_NEXTTRACE_BIN")
}

// ── view ─────────────────────────────────────────────────────────────────────

func (d *diagnosticsModel) view(width, height int) string {
	var b strings.Builder
	b.WriteString(pageTitle(tabDiagnostics, "Route Diagnostics (NextTrace)"))
	b.WriteByte('\n')
	if d.editing {
		b.WriteString("Target: ")
		cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
		b.WriteString(listSelected.Render(d.target) + cursor)
		b.WriteByte('\n')
		b.WriteString(kbdHints("Enter", "run", "Esc", "cancel"))
		b.WriteByte('\n')
	} else {
		b.WriteString("Target: " + d.target)
		b.WriteByte('\n')
		b.WriteString(kbdHints(
			"i", "edit target",
			"s", "stop",
			"c", "clear",
		))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	d.mu.Lock()
	out := append([]string(nil), d.output...)
	d.mu.Unlock()
	maxOut := height - 8
	if maxOut < 4 {
		maxOut = 4
	}
	if len(out) > maxOut {
		out = out[len(out)-maxOut:]
	}
	if len(out) == 0 {
		b.WriteString(dimText.Render("(no output)"))
	} else {
		b.WriteString(strings.Join(out, "\n"))
	}
	return b.String()
}

// Override update for the batch case (avoids changing the type-switch above
// to keep the file readable; message arrives via the same pipeline).
func (d *diagnosticsModel) handleTraceBatch(b traceBatchMsg) tea.Cmd {
	d.mu.Lock()
	d.output = append(d.output, b.lines...)
	d.mu.Unlock()
	d.running = false
	if b.err != nil && b.err != context.Canceled {
		return d.root.setFlash(false, "trace: "+b.err.Error())
	}
	return d.root.setFlash(true, "trace finished")
}
