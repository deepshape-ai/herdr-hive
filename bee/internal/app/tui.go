package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
	"golang.org/x/term"
)

type panelSnapshot struct {
	config      config.Config
	status      publisher.Status
	sessions    []herdr.Session
	err         error
	sessionsErr error
	epoch       int
}
type panelTick struct{}
type panelDone struct {
	err           error
	update, saved bool
}
type panelClick struct {
	target, page int
	session      string
}

type panelModel struct {
	dirtyFields                                            [3]bool
	ctx                                                    context.Context
	app                                                    App
	snapshot                                               panelSnapshot
	fields                                                 []textinput.Model
	viewport                                               viewport.Model
	width, height, page, focus, epoch                      int
	lastFocus, lastPage, lastHeight                        int
	ready, refreshing, busy, editing, dirty, dark, restart bool
	message                                                string
	messageError                                           bool
	read                                                   func(int) tea.Cmd
	execute                                                func([][]string) tea.Cmd
}

func (a App) TUI(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("TUI needs a terminal; use bee help for noninteractive commands")
	}
	uiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newPanel(uiCtx, a)
	result, err := tea.NewProgram(m, tea.WithContext(uiCtx), tea.WithOutput(a.Out)).Run()
	if err != nil {
		return err
	}
	if result.(panelModel).restart {
		return publisher.ErrRestart
	}
	return nil
}

func newPanel(ctx context.Context, a App) panelModel {
	m := panelModel{ctx: ctx, app: a, width: 60, height: 30, viewport: viewport.New()}
	for _, placeholder := range []string{"hive.example.internal:2222", "Your device name", "/absolute/path/to/hive_device"} {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		input.CharLimit = 4096
		input.SetVirtualCursor(true)
		m.fields = append(m.fields, input)
	}
	m.read = func(epoch int) tea.Cmd {
		return func() tea.Msg {
			s := panelSnapshot{epoch: epoch}
			s.config, s.err = config.Load(a.Dir)
			if s.err != nil {
				return s
			}
			var out bytes.Buffer
			err := (App{Dir: a.Dir, Out: &out}).Execute(ctx, []string{"status"})
			if err == nil {
				err = json.Unmarshal(out.Bytes(), &s.status)
			}
			if err != nil {
				s.status.Error = err.Error()
			}
			s.sessions, s.sessionsErr = herdr.Sessions()
			s.sessions = sessionChoices(s.sessions, s.config.Rules)
			return s
		}
	}
	m.execute = func(commands [][]string) tea.Cmd {
		return func() tea.Msg {
			for _, args := range commands {
				exe, err := a.executable()
				if err != nil {
					return panelDone{err: err}
				}
				cmd := exec.CommandContext(ctx, exe, args...)
				cmd.Env = append(os.Environ(), "BEE_CONFIG_DIR="+a.Dir)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				// Command JSON belongs to the CLI. It must never be mixed into terminal frames.
				if err = cmd.Run(); err != nil {
					detail := strings.TrimSpace(stderr.String())
					if detail == "" {
						detail = err.Error()
					}
					return panelDone{err: fmt.Errorf("%s", detail)}
				}
			}
			return panelDone{update: len(commands) == 1 && commands[0][0] == "update", saved: commands[0][0] == "configure" || commands[0][0] == "name"}
		}
	}
	return m
}

func (m panelModel) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, func() tea.Msg { return panelTick{} })
}
func panelTimer() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return panelTick{} })
}
func (m *panelModel) refresh() tea.Cmd {
	if m.refreshing || m.busy {
		return nil
	}
	m.refreshing = true
	return m.read(m.epoch)
}
func (m *panelModel) run(commands ...[]string) tea.Cmd {
	if m.busy || !m.ready {
		return nil
	}
	m.busy = true
	m.epoch++
	m.message, m.messageError = "Saving changes…", false
	if commands[0][0] == "update" {
		m.message = "Downloading update. Sharing stays online until activation…"
	}
	return m.execute(commands)
}
func (m *panelModel) syncFields() {
	c := m.snapshot.config
	for i, value := range []string{c.Hive, c.Name, c.IdentityFile} {
		if !m.dirtyFields[i] && !(m.editing && m.focus == i) {
			m.fields[i].SetValue(value)
		}
	}
}
func (m panelModel) itemCount() int {
	if m.page == 0 {
		return 4
	}
	return len(m.snapshot.sessions) + 1
}
func (m *panelModel) move(delta int) {
	m.focus = (m.focus + delta + m.itemCount()) % m.itemCount()
}
func (m *panelModel) activate() tea.Cmd {
	if m.busy || !m.ready {
		return nil
	}
	if m.page == 0 {
		if m.focus < 3 {
			m.editing = true
			return m.fields[m.focus].Focus()
		}
		return m.save()
	}
	if m.focus == len(m.snapshot.sessions) {
		return m.toggle()
	}
	name := m.snapshot.sessions[m.focus].Name
	op := "share"
	for _, r := range m.snapshot.config.Rules {
		if r.Session == name {
			op = "unshare"
		}
	}
	if op == "share" && (!m.snapshot.sessions[m.focus].Running || m.snapshot.sessionsErr != nil) {
		m.message, m.messageError = "Only running sessions can be shared.", true
		return nil
	}
	return m.run([]string{op, name})
}
func (m *panelModel) save() tea.Cmd {
	if m.page != 0 || !m.dirty {
		return nil
	}
	c := m.snapshot.config
	values := []string{}
	values = []string{c.Hive, c.Name, c.IdentityFile}
	for i, field := range m.fields {
		if m.dirtyFields[i] {
			values[i] = strings.TrimSpace(field.Value())
		}
	}
	commands := [][]string{}
	if values[0] != c.Hive || values[2] != c.IdentityFile {
		commands = append(commands, []string{"configure", "--hive", values[0], "--identity", values[2]})
	}
	if values[1] != c.Name {
		commands = append(commands, []string{"name", values[1]})
	}
	if len(commands) == 0 {
		m.dirty = false
		m.dirtyFields = [3]bool{}
		return nil
	}
	return m.run(commands...)
}
func (m *panelModel) toggle() tea.Cmd {
	op := "enable"
	if m.snapshot.config.Enabled {
		op = "disable"
	}
	return m.run([]string{op})
}
func (m panelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for i := range m.fields {
			m.fields[i].SetWidth(max(4, m.width-10))
		}
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		for i := range m.fields {
			m.fields[i].SetStyles(textinput.DefaultStyles(m.dark))
		}
	case panelTick:
		cmd = tea.Batch(m.refresh(), panelTimer())
	case panelSnapshot:
		m.refreshing = false
		if msg.epoch != m.epoch || m.busy {
			break
		}
		selectedName := ""
		if m.page == 1 && m.focus < len(m.snapshot.sessions) {
			selectedName = m.snapshot.sessions[m.focus].Name
		}
		previousError := m.snapshot.err
		m.snapshot = msg
		if msg.err != nil {
			m.ready = false
			m.message, m.messageError = msg.err.Error(), true
			break
		}
		if previousError != nil {
			m.message = ""
			m.messageError = false
		}
		if selectedName != "" {
			for i, s := range msg.sessions {
				if s.Name == selectedName {
					m.focus = i
					break
				}
			}
		}
		m.syncFields()
		m.ready = true
		m.focus = min(m.focus, m.itemCount()-1)
	case panelDone:
		m.busy = false
		if msg.err != nil {
			m.message, m.messageError = msg.err.Error(), true
		} else {
			m.message, m.messageError = "Changes saved.", false
			if msg.saved {
				m.dirty = false
				m.dirtyFields = [3]bool{}
			}
			if msg.update {
				m.restart = true
				return m, tea.Quit
			}
		}
		cmd = m.refresh()
	case panelClick:
		if msg.target >= 0 || msg.target == -3 {
			if msg.page != m.page {
				return m, nil
			}
			if msg.session != "" {
				found := -1
				for i, s := range m.snapshot.sessions {
					if s.Name == msg.session {
						found = i
						break
					}
				}
				if found < 0 {
					return m, nil
				}
				msg.target = found
			}
			if msg.target >= m.itemCount() {
				return m, nil
			}
		}
		if m.editing {
			m.fields[m.focus].Blur()
			m.editing = false
		}
		switch msg.target {
		case -1, -2:
			m.page = -msg.target - 1
			m.focus = 0
			m.viewport.GotoTop()
		case -3:
			cmd = m.toggle()
		case -4:
			cmd = m.run([]string{"update"})
		default:
			m.focus = msg.target
			cmd = m.activate()
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.editing {
			switch key {
			case "esc", "enter":
				m.fields[m.focus].Blur()
				m.editing = false
			case "tab", "shift+tab":
				m.fields[m.focus].Blur()
				delta := 1
				if key == "shift+tab" {
					delta = -1
				}
				m.move(delta)
				m.editing = m.focus < 3
				if m.editing {
					cmd = m.fields[m.focus].Focus()
				}
			case "ctrl+s":
				m.fields[m.focus].Blur()
				m.editing = false
				cmd = m.save()
			default:
				cmd = m.updateField(msg)
			}
			break
		}
		switch key {
		case "q", "esc":
			return m, tea.Quit
		case "1", "2", "left", "right":
			if key == "1" {
				m.page = 0
			} else if key == "2" {
				m.page = 1
			} else {
				m.page = 1 - m.page
			}
			m.focus = 0
			m.viewport.GotoTop()
		case "tab", "down", "j":
			m.move(1)
		case "shift+tab", "up", "k":
			m.move(-1)
		case "enter", "space":
			cmd = m.activate()
		case "ctrl+s":
			cmd = m.save()
		case "s":
			cmd = m.toggle()
		case "u":
			cmd = m.run([]string{"update"})
		case "r":
			cmd = m.refresh()
		case "pgdown":
			m.viewport.PageDown()
		case "pgup":
			m.viewport.PageUp()
		}
	case tea.PasteMsg:
		if m.editing {
			cmd = m.updateField(msg)
		}
	default:
		if m.editing {
			cmd = m.updateField(msg)
		}
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			m.viewport, cmd = m.viewport.Update(msg)
		}
	}
	m.layout()
	return m, cmd
}
func (m *panelModel) updateField(msg tea.Msg) tea.Cmd {
	before := m.fields[m.focus].Value()
	field, cmd := m.fields[m.focus].Update(msg)
	m.fields[m.focus] = field
	if field.Value() != before {
		m.dirty = true
		m.dirtyFields[m.focus] = true
	}
	return cmd
}
func sessionChoices(live []herdr.Session, rules []config.Rule) []herdr.Session {
	seen := map[string]bool{}
	for _, s := range live {
		seen[s.Name] = true
	}
	out := append([]herdr.Session(nil), live...)
	for _, r := range rules {
		if !seen[r.Session] {
			out = append(out, herdr.Session{Name: r.Session})
			seen[r.Session] = true
		}
	}
	return out
}
func safePanel(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
