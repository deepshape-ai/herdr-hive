package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

func TestHiveDirectoryRefreshLifecycle(t *testing.T) {
	m := readyPanel()
	calls := 0
	m.readHive = func(context.Context, config.Config) ([]publisher.Share, error) {
		calls++
		return []publisher.Share{{Name: "remote", Label: "default", ID: "s-1"}}, nil
	}
	if m.refreshHive(true) != nil {
		t.Fatal("queried hidden tab")
	}
	m.page = 2
	cmd := m.refreshHive(false)
	if cmd == nil || !m.hive.loading {
		t.Fatal("missing initial load")
	}
	if m.refreshHive(true) != nil {
		t.Fatal("overlapping query")
	}
	result := cmd().(panelHive)
	next, _ := m.Update(result)
	m = next.(panelModel)
	if !m.hive.loaded || m.hive.loading || len(m.hive.shares) != 1 || calls != 1 {
		t.Fatal("query did not populate tab")
	}
	if m.refreshHive(false) != nil {
		t.Fatal("poll interval not enforced")
	}
	failure := m.refreshHive(true)().(panelHive)
	failure.err = errors.New("offline")
	next, _ = m.Update(failure)
	m = next.(panelModel)
	body, _ := m.body()
	if !strings.Contains(ansi.Strip(body), "Last known") || len(m.hive.shares) != 1 {
		t.Fatal("stale data not labelled")
	}
	m.snapshot.config.Hive = "another:2222"
	fresh := m.refreshHive(true)
	if fresh == nil || m.hive.loaded || len(m.hive.shares) != 0 {
		t.Fatal("old Hive data retained")
	}
	next, _ = m.Update(result)
	m = next.(panelModel)
	if m.hive.loaded || !m.hive.loading {
		t.Fatal("stale response accepted")
	}
}

func TestHiveNavigationAndReadOnlyRows(t *testing.T) {
	m := readyPanel()
	m.snapshot.status.Connected = true
	m.readHive = func(context.Context, config.Config) ([]publisher.Share, error) { return nil, nil }
	commands := 0
	m.execute = func([][]string) tea.Cmd { commands++; return nil }
	next, _ := m.Update(keyPress('3'))
	m = next.(panelModel)
	if m.page != 2 {
		t.Fatal("third tab inaccessible")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(panelModel)
	if commands != 0 || m.editing {
		t.Fatal("directory activation changed sharing")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(panelModel)
	if m.page != 0 {
		t.Fatal("right did not wrap")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(panelModel)
	if m.page != 2 {
		t.Fatal("left did not wrap")
	}
	for _, width := range []int{64, 40, 28} {
		m.width = width
		m.page = 0
		m.layout()
		frame := ansi.Strip(m.View().Content)
		first := strings.Split(frame, "\n")[0]
		if strings.Contains(first, "Sharing") {
			t.Fatal("Sharing remains in title")
		}
		// Find a click target in the third tab at every supported tab layout.
		found := false
		for x := 2; x < width-2; x++ {
			cmd := m.View().OnMouse(tea.MouseClickMsg{X: x, Y: 3, Button: tea.MouseLeft})
			if cmd != nil && cmd().(panelClick).target == -5 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("third tab has no hit target at %d", width)
		}
	}
}

func TestHiveGroupsSessionsAndLocalConnection(t *testing.T) {
	m := readyPanel()
	m.snapshot.status = publisher.Status{Connected: true, Shares: []publisher.Share{{Name: "local", Label: "default"}}}
	m.hive.shares = []publisher.Share{{Name: "zulu", Label: "two"}, {Name: "alpha", Label: "main"}, {Name: "zulu", Label: "one"}}
	bees := m.hiveBees()
	if len(bees) != 3 || !bees[0].local || bees[1].name != "alpha" || strings.Join(bees[2].sessions, ",") != "one,two" {
		t.Fatalf("incorrect grouping: %+v", bees)
	}
	m.snapshot.status.Connected = false
	if len(m.hiveBees()) != 2 {
		t.Fatal("disconnected local Bee presented as online")
	}
}
