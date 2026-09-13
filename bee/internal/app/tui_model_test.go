package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

func readyPanel() panelModel {
	m := newPanel(context.Background(), App{Version: "test"})
	m.ready = true
	m.snapshot = panelSnapshot{config: config.Config{Name: "Design workstation", Hive: "hive.example.internal:2222", IdentityFile: "/home/member/.ssh/hive_device", KnownHosts: "/home/member/.ssh/known_hosts"}, sessions: []herdr.Session{{Name: "project-api", Running: true}, {Name: "design-review", Running: true}}}
	m.syncFields()
	m.layout()
	return m
}
func keyPress(key rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: key, Text: string(key)} }
func TestPanelRefreshPreservesDraftAndSelection(t *testing.T) {
	m := readyPanel()
	m.dirty = true
	m.dirtyFields[1] = true
	m.fields[1].SetValue("Unsaved name")
	m.page = 1
	m.focus = 1
	s := m.snapshot
	s.config.Name = "Changed through CLI"
	s.sessions = []herdr.Session{m.snapshot.sessions[1], m.snapshot.sessions[0]}
	s.status.Connected = true
	result, _ := m.Update(s)
	m = result.(panelModel)
	if m.fields[1].Value() != "Unsaved name" || m.focus != 0 || !m.snapshot.status.Connected {
		t.Fatal("refresh lost input, selection or status")
	}
	m.epoch++
	s.status.Connected = false
	result, _ = m.Update(s)
	if !result.(panelModel).snapshot.status.Connected {
		t.Fatal("stale refresh overwrote action state")
	}
}
func TestPanelRefreshDoesNotOverlap(t *testing.T) {
	m := readyPanel()
	calls := 0
	m.read = func(int) tea.Cmd { calls++; return nil }
	m.refresh()
	m.refresh()
	if calls != 1 {
		t.Fatal("overlapping refresh")
	}
}
func TestPanelEditingShortcutsAndPaste(t *testing.T) {
	m := readyPanel()
	m.focus = 1
	m.activate()
	for _, r := range "qsur" {
		result, _ := m.Update(keyPress(r))
		m = result.(panelModel)
	}
	if !m.editing || !m.dirty || !strings.Contains(m.fields[1].Value(), "qsur") {
		t.Fatal("global shortcuts intercepted field input")
	}
	result, _ := m.Update(tea.PasteMsg{Content: " pasted"})
	m = result.(panelModel)
	if !strings.Contains(m.fields[1].Value(), "pasted") {
		t.Fatal("paste was lost")
	}
}
func TestNameSaveDoesNotRequireHiveConfiguration(t *testing.T) {
	m := readyPanel()
	m.snapshot.config.Hive = ""
	m.snapshot.config.IdentityFile = ""
	m.snapshot.config.KnownHosts = ""
	m.syncFields()
	m.fields[1].SetValue("New name")
	m.dirty = true
	m.dirtyFields[1] = true
	var commands [][]string
	m.execute = func(args [][]string) tea.Cmd { commands = args; return nil }
	m.save()
	if len(commands) != 1 || commands[0][0] != "name" {
		t.Fatalf("unexpected commands: %v", commands)
	}
}
func TestSharingActionPreservesDraft(t *testing.T) {
	m := readyPanel()
	m.dirty = true
	m.dirtyFields[1] = true
	m.fields[1].SetValue("draft")
	result, _ := m.Update(panelDone{})
	if !result.(panelModel).dirty {
		t.Fatal("sharing action discarded form draft")
	}
}
func TestMissingSessionCanBeUnsharedWhileDiscoveryFails(t *testing.T) {
	m := readyPanel()
	m.page = 1
	m.snapshot.sessionsErr = errors.New("offline")
	m.snapshot.config.Rules = []config.Rule{{Session: "missing"}}
	m.snapshot.sessions = sessionChoices(nil, m.snapshot.config.Rules)
	var command []string
	m.execute = func(args [][]string) tea.Cmd { command = args[0]; return nil }
	m.activate()
	if len(command) != 2 || command[0] != "unshare" || command[1] != "missing" {
		t.Fatalf("%v", command)
	}
}
func TestPanelFramesFitAndExposeControls(t *testing.T) {
	for _, size := range [][2]int{{100, 36}, {60, 30}, {40, 20}, {28, 12}, {20, 8}} {
		for _, dark := range []bool{false, true} {
			for page := 0; page < 2; page++ {
				m := readyPanel()
				m.page = page
				m.dark = dark
				m.width = size[0]
				m.height = size[1]
				m.snapshot.config.Name = "设备 " + strings.Repeat("Long device name ", 30)
				m.snapshot.sessions[0].Name = "项目 " + strings.Repeat("long-name", 30)
				m.message = strings.Repeat("connection failed ", 30)
				m.layout()
				frame := m.View().Content
				if lipgloss.Width(frame) > m.width || lipgloss.Height(frame) > m.height {
					t.Fatalf("frame overflow %v page %d: %dx%d", size, page, lipgloss.Width(frame), lipgloss.Height(frame))
				}
				if size[0] >= 28 && !strings.Contains(frame, "q close") {
					t.Fatal("close action disappeared")
				}
			}
		}
	}
}
func TestPanelFailureRecoversOnRefresh(t *testing.T) {
	m := readyPanel()
	s := m.snapshot
	s.err = errors.New("invalid configuration")
	result, _ := m.Update(s)
	m = result.(panelModel)
	if m.ready {
		t.Fatal("invalid configuration still editable")
	}
	s.err = nil
	result, _ = m.Update(s)
	m = result.(panelModel)
	if !m.ready || m.messageError {
		t.Fatal("recovered configuration still displays error")
	}
}
func TestPanelRenderFixtures(t *testing.T) {
	folder := os.Getenv("BEE_TEST_RENDER_DIR")
	if folder == "" {
		t.Skip("optional visual evidence")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		for page := 0; page < 2; page++ {
			m := readyPanel()
			m.page = page
			m.dark = dark
			m.width = 64
			m.height = 30
			m.snapshot.config.Enabled = true
			m.snapshot.status = publisher.Status{Connected: true}
			m.snapshot.config.Rules = []config.Rule{{Session: "project-api"}}
			m.focus = page
			m.layout()
			if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("panel-%d-%t.ansi", page, dark)), []byte(m.View().Content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestPanelDraftMergesUneditedExternalFields(t *testing.T) {
	m := readyPanel()
	m.fields[1].SetValue("Draft name")
	m.dirty = true
	m.dirtyFields[1] = true
	s := m.snapshot
	s.config.Hive = "new-hive.example.internal:2222"
	s.config.IdentityFile = "/new/identity"
	updated, _ := m.Update(s)
	m = updated.(panelModel)
	if m.fields[0].Value() != s.config.Hive {
		t.Fatal("unedited field did not refresh")
	}
	var commands [][]string
	m.execute = func(args [][]string) tea.Cmd { commands = args; return nil }
	m.save()
	if len(commands) != 1 || commands[0][0] != "name" {
		t.Fatalf("saving name rewrote external connection: %v", commands)
	}
}
func TestPanelMouseUsesSessionIdentity(t *testing.T) {
	for _, scenario := range []string{"reordered", "removed", "page changed"} {
		t.Run(scenario, func(t *testing.T) {
			m := readyPanel()
			m.page = 1
			m.layout()
			_, rows := m.body()
			click := m.View().OnMouse(tea.MouseClickMsg{X: 4, Y: 5 + rows[1] - m.viewport.YOffset(), Button: tea.MouseLeft})
			if click == nil {
				t.Fatal("no session hit target")
			}
			snapshot := m.snapshot
			if scenario == "reordered" {
				snapshot.sessions = []herdr.Session{snapshot.sessions[1], snapshot.sessions[0]}
			} else {
				snapshot.sessions = nil
			}
			next, _ := m.Update(snapshot)
			m = next.(panelModel)
			if scenario == "page changed" {
				m.page = 0
			}
			var commands [][]string
			m.execute = func(args [][]string) tea.Cmd { commands = args; return nil }
			m.Update(click())
			if scenario == "reordered" {
				if len(commands) != 1 || commands[0][1] != "design-review" {
					t.Fatalf("wrong session %v", commands)
				}
			} else if len(commands) != 0 {
				t.Fatalf("stale click acted: %v", commands)
			}
		})
	}
}
func TestPanelSmallViewportKeepsSelectedSessionVisible(t *testing.T) {
	m := readyPanel()
	m.width = 28
	m.height = 12
	m.page = 1
	m.layout()
	_, rows := m.body()
	if m.viewport.YOffset() > rows[m.focus] || m.viewport.YOffset()+m.viewport.Height() <= rows[m.focus] {
		t.Fatal("selection outside viewport")
	}
}

func TestPanelOmitsKnownHostsControl(t *testing.T) {
	m := readyPanel()
	if len(m.fields) != 3 || m.itemCount() != 4 {
		t.Fatal("unexpected connection controls")
	}
	if strings.Contains(m.View().Content, "known_hosts") {
		t.Fatal("advanced SSH setting exposed in panel")
	}
}
