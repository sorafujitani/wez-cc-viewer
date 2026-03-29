package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Colors ──────────────────────────────────────────────

var (
	cPurple  = lipgloss.Color("141")
	cGreen   = lipgloss.Color("114")
	cCyan    = lipgloss.Color("75")
	cGray    = lipgloss.Color("246")
	cDimGray = lipgloss.Color("240")
	cWhite   = lipgloss.Color("255")
	cSelBg   = lipgloss.Color("237")
)

// ── Styles ──────────────────────────────────────────────

var (
	sHeaderBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cDimGray).
			Padding(0, 1)
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cPurple)
	sStats  = lipgloss.NewStyle().Foreground(cGray).Faint(true)
	sActive = lipgloss.NewStyle().Foreground(cCyan)
	sIdle   = lipgloss.NewStyle().Foreground(cDimGray)
	sName   = lipgloss.NewStyle().Foreground(cGray)
	sSelRow = lipgloss.NewStyle().Background(cSelBg).Foreground(cWhite).Bold(true)
	sSep    = lipgloss.NewStyle().Foreground(cDimGray)
	sLabel  = lipgloss.NewStyle().Foreground(cDimGray)
	sValue  = lipgloss.NewStyle().Foreground(cWhite)
	sDir    = lipgloss.NewStyle().Foreground(cCyan)
	sHint   = lipgloss.NewStyle().Foreground(cGray)
	sEmpty  = lipgloss.NewStyle().Foreground(cGray).Faint(true)
)

// ── Data ────────────────────────────────────────────────

// Agent represents a detected Claude Code instance running in a WezTerm pane.
type Agent struct {
	PaneID    int
	Workspace string
	Project   string
	CWD       string
	Title     string
	Status    string // "running" | "idle"
}

type wezPane struct {
	PaneID    int    `json:"pane_id"`
	Workspace string `json:"workspace"`
	CWD       string `json:"cwd"`
	Title     string `json:"title"`
	TTYName   string `json:"tty_name"`
}

type psProc struct {
	ppid int
	tty  string
	name string
}

// ── Scanner ─────────────────────────────────────────────

var (
	weztermBin   = findWezterm()
	titleCleanRe = regexp.MustCompile(`^[\s\x{2800}-\x{28ff}\x{2733}-\x{2735}✳●○◉]+`)
)

func findWezterm() string {
	if p, err := exec.LookPath("wezterm"); err == nil {
		return p
	}
	for _, p := range []string{
		"/opt/homebrew/bin/wezterm",
		"/usr/local/bin/wezterm",
		"/Applications/WezTerm.app/Contents/MacOS/wezterm",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "wezterm"
}

func myTTY() string {
	if link, err := os.Readlink("/dev/fd/0"); err == nil {
		return link
	}
	return ""
}

// Scan detects Claude Code instances across all WezTerm panes.
//
// Detection works by cross-referencing the WezTerm pane list (via wezterm cli)
// with the OS process tree (via ps). A pane is considered a Claude Code agent
// if any process on its TTY has "claude" in its ancestor chain.
//
// Running vs idle is determined by the presence of a "caffeinate" child process
// under the Claude process — Claude Code spawns caffeinate while actively working
// and kills it when idle.
func Scan() []Agent {
	exclude := myTTY()

	out, err := exec.Command(weztermBin, "cli", "list", "--format", "json").Output()
	if err != nil {
		return nil
	}
	var panes []wezPane
	if json.Unmarshal(out, &panes) != nil {
		return nil
	}

	psOut, err := exec.Command("ps", "-eo", "pid,ppid,tty,comm").Output()
	if err != nil {
		return nil
	}

	procs := map[int]psProc{}
	children := map[int][]int{}
	claudePIDs := map[int]bool{}

	for _, line := range strings.Split(string(psOut), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		comm := strings.Join(f[3:], " ")
		name := comm
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		procs[pid] = psProc{ppid: ppid, tty: f[2], name: name}
		children[ppid] = append(children[ppid], pid)
		if strings.Contains(strings.ToLower(name), "claude") {
			claudePIDs[pid] = true
		}
	}

	ancestor := func(pid int) int {
		visited := map[int]bool{}
		for cur := pid; cur > 1 && !visited[cur]; {
			visited[cur] = true
			if claudePIDs[cur] {
				return cur
			}
			if p, ok := procs[cur]; ok {
				cur = p.ppid
			} else {
				break
			}
		}
		return -1
	}

	claudeStatus := map[int]string{}
	for cpid := range claudePIDs {
		s := "idle"
		for _, ch := range children[cpid] {
			if procs[ch].name == "caffeinate" {
				s = "running"
				break
			}
		}
		claudeStatus[cpid] = s
	}

	home, _ := os.UserHomeDir()
	var agents []Agent
	seen := map[int]bool{}

	for _, p := range panes {
		if exclude != "" && p.TTYName == exclude {
			continue
		}
		tty := strings.TrimPrefix(p.TTYName, "/dev/")
		matched := -1
		for pid, proc := range procs {
			if proc.tty == tty {
				if cpid := ancestor(pid); cpid > 0 && !seen[cpid] {
					matched = cpid
					break
				}
			}
		}
		if matched < 0 {
			continue
		}
		seen[matched] = true

		cwd := p.CWD
		if u, err := url.Parse(cwd); err == nil && u.Scheme == "file" {
			cwd = u.Path
		}
		project := cwd
		trimmed := strings.TrimRight(cwd, "/")
		if i := strings.LastIndex(trimmed, "/"); i >= 0 {
			project = trimmed[i+1:]
		}
		title := strings.TrimSpace(titleCleanRe.ReplaceAllString(p.Title, ""))
		if title == "" {
			title = project
		}
		displayCWD := cwd
		if home != "" {
			displayCWD = strings.Replace(cwd, home, "~", 1)
		}

		agents = append(agents, Agent{
			PaneID:    p.PaneID,
			Workspace: p.Workspace,
			Project:   project,
			CWD:       displayCWD,
			Title:     title,
			Status:    claudeStatus[matched],
		})
	}

	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Status != agents[j].Status {
			return agents[i].Status == "running"
		}
		return agents[i].Workspace < agents[j].Workspace
	})
	return agents
}

// sendUserVar sends an iTerm2/WezTerm SetUserVar escape sequence.
// WezTerm fires the "user-var-changed" Lua event when it receives this,
// enabling workspace switching from the Lua config side.
func sendUserVar(name, value string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	seq := fmt.Sprintf("\033]1337;SetUserVar=%s=%s\007", name, encoded)

	// Write directly to /dev/tty to bypass bubbletea's output management
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		fmt.Fprint(f, seq)
		f.Sync()
		f.Close()
	}
}

// ── Bubbletea ───────────────────────────────────────────

type model struct {
	agents   []Agent
	sel      int
	w, h     int
	switchTo string
}

type scanMsg []Agent
type tickMsg time.Time

func doScan() tea.Msg { return scanMsg(Scan()) }

func tick() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(doScan, tick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

	case tea.KeyMsg:
		n := len(m.agents)
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if n > 0 {
				m.sel = (m.sel + 1) % n
			}
		case "k", "up":
			if n > 0 {
				m.sel = (m.sel - 1 + n) % n
			}
		case "enter":
			if m.sel < n {
				a := m.agents[m.sel]
				sendUserVar("switch_workspace", a.Workspace)
				m.switchTo = a.Workspace
				return m, tea.Quit
			}
		case "r":
			return m, doScan
		case "g":
			m.sel = 0
		case "G":
			m.sel = max(0, n-1)
		}

	case scanMsg:
		m.agents = []Agent(msg)
		if m.sel >= len(m.agents) {
			m.sel = max(0, len(m.agents)-1)
		}

	case tickMsg:
		return m, tea.Batch(doScan, tick())
	}
	return m, nil
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	w := min(m.w-4, 68)
	total := len(m.agents)

	var b strings.Builder
	b.WriteString(m.viewHeader(w))
	b.WriteString("\n\n")

	if total == 0 {
		b.WriteString(sEmpty.Render("   No agents detected"))
		b.WriteString("\n")
		b.WriteString(sEmpty.Render("   Start Claude Code in any workspace to see it here."))
		b.WriteString("\n")
	} else {
		for i, a := range m.agents {
			b.WriteString(m.viewRow(i, a, w))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	if total > 0 && m.sel < total {
		b.WriteString(m.viewDetail(w))
		b.WriteString("\n")
	}

	b.WriteString(m.viewFooter())
	return b.String()
}

func (m model) viewHeader(w int) string {
	running := 0
	for _, a := range m.agents {
		if a.Status == "running" {
			running++
		}
	}
	title := sTitle.Render("🤖 wez-cc-viewer")
	stats := fmt.Sprintf("%d agents", len(m.agents))
	if running > 0 {
		stats += fmt.Sprintf(" · %d active", running)
	}
	right := sStats.Render(stats)

	gap := max(1, w-lipgloss.Width(title)-lipgloss.Width(right)-4)
	return sHeaderBox.Width(w).Render(title + strings.Repeat(" ", gap) + right)
}

func (m model) viewRow(i int, a Agent, w int) string {
	proj := a.Project
	if len(proj) > 22 {
		proj = proj[:22]
	}
	isSel := i == m.sel

	// Status colors are always preserved regardless of selection
	var dot, st string
	if a.Status == "running" {
		dot = sActive.Render("●")
		st = sActive.Render("active")
	} else {
		dot = sIdle.Render("○")
		st = sIdle.Render("idle  ")
	}

	var cursor, name, ws string
	if isSel {
		cursor = " ▸"
		name = lipgloss.NewStyle().Bold(true).Foreground(cWhite).Render(fmt.Sprintf("%-22s", proj))
		ws = lipgloss.NewStyle().Foreground(cGray).Render(a.Workspace)
	} else {
		cursor = "  "
		name = sName.Render(fmt.Sprintf("%-22s", proj))
		ws = sIdle.Render(a.Workspace)
	}

	content := fmt.Sprintf("  %s %s %s  %s  %s", cursor, dot, name, st, ws)
	if isSel {
		// Pad and apply background
		padded := content + strings.Repeat(" ", max(0, w+2-lipgloss.Width(content)))
		return lipgloss.NewStyle().Background(cSelBg).Render(padded)
	}
	return content
}

func (m model) viewDetail(w int) string {
	a := m.agents[m.sel]
	sep := sSep.Render(strings.Repeat("─", w))
	title := a.Title
	if len(title) > w-12 {
		title = title[:w-12]
	}
	return fmt.Sprintf("%s\n   %s   %s\n   %s    %s\n   %s   %s\n%s",
		sep,
		sLabel.Render("Task"), sValue.Render(title),
		sLabel.Render("Dir"), sDir.Render(a.CWD),
		sLabel.Render("Pane"), sLabel.Render(fmt.Sprintf("#%d", a.PaneID)),
		sep)
}

func (m model) viewFooter() string {
	return fmt.Sprintf("   %s navigate   %s switch   %s refresh   %s quit",
		sHint.Render("↑↓"), sHint.Render("⏎"), sHint.Render("r"), sHint.Render("q"))
}

// ── Main ────────────────────────────────────────────────

func main() {
	p := tea.NewProgram(model{}, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if final, ok := result.(model); ok && final.switchTo != "" {
		// Fallback: also send via stdout after bubbletea exits
		encoded := base64.StdEncoding.EncodeToString([]byte(final.switchTo))
		fmt.Printf("\033]1337;SetUserVar=%s=%s\007", "switch_workspace", encoded)
		os.Stdout.Sync()
		time.Sleep(100 * time.Millisecond)
	}
}
