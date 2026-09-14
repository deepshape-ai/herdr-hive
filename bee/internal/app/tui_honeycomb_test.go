package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

func TestHoneycombFitsAndKeepsEverySession(t *testing.T) {
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || strings.ContainsRune("╱╲─│", r) {
				return -1
			}
			return r
		}, ansi.Strip(s))
	}
	for _, width := range []int{8, 24, 36, 49, 50, 60, 75, 76, 96, 120} {
		for _, dark := range []bool{false, true} {
			for _, count := range []int{1, 2, 3, 7} {
				var bees []hiveBee
				for i := 0; i < count; i++ {
					bees = append(bees, hiveBee{name: fmt.Sprintf("设备-%d-with-a-long-name", i), local: i == 0, sessions: []string{"default", fmt.Sprintf("review-%d", i), "very-long-session-with-many-characters"}})
				}
				frame := hiveHoneycomb(bees, width, stylesFor(dark), false)
				if lipgloss.Width(frame) > width {
					t.Fatalf("%d Bees overflow width %d", count, width)
				}
				for _, bee := range bees {
					text := compact(strings.Join(hiveCell(bee, min(24, width), stylesFor(dark), false), "\n"))
					if !strings.Contains(text, compact(bee.name)) {
						t.Fatal("Bee name was lost")
					}
					for _, session := range bee.sessions {
						if !strings.Contains(text, compact(session)) {
							t.Fatal("session name was lost")
						}
					}
				}
			}
		}
	}
}

func TestHoneycombStaleStateIsExplicit(t *testing.T) {
	cell := strings.Join(hiveCell(hiveBee{name: "remote", sessions: []string{"default"}}, 24, stylesFor(false), true), "\n")
	if !strings.Contains(ansi.Strip(cell), "LAST KNOWN") || strings.Contains(ansi.Strip(cell), "CONNECTED") {
		t.Fatal("stale Bee appears connected")
	}
}

func TestHoneycombRenderMatrix(t *testing.T) {
	folder := os.Getenv("BEE_TEST_RENDER_DIR")
	if folder == "" {
		t.Skip("optional visual evidence")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{64, 30}, {54, 30}, {53, 30}, {80, 30}, {79, 30}, {28, 36}, {64, 12}, {28, 12}, {100, 36}} {
		for _, dark := range []bool{false, true} {
			m := readyPanel()
			m.page = 2
			m.width = size[0]
			m.height = size[1]
			m.dark = dark
			m.snapshot.config.Name = "carol"
			m.snapshot.status = publisher.Status{Connected: true, Shares: []publisher.Share{{Name: "carol", Label: "default"}}}
			m.hive = hiveDirectory{loaded: true, panelHive: panelHive{target: hiveTarget{"hive.example.internal:2222", "/key", "/known"}, shares: []publisher.Share{{Name: "ltq", Label: "default"}, {Name: "ringo", Label: "default"}}}}
			m.layout()
			frame := m.View().Content
			if lipgloss.Width(frame) > m.width || lipgloss.Height(frame) > m.height {
				t.Fatalf("overflow at %v", size)
			}
			name := fmt.Sprintf("honeycomb-%dx%d-%t.ansi", m.width, m.height, dark)
			if err := os.WriteFile(filepath.Join(folder, name), []byte(frame), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHoneycombPlanAAlignedWrappingWithoutFill(t *testing.T) {
	bees := []hiveBee{
		{name: "carol", local: true, sessions: []string{"default"}},
		{name: "ltq", sessions: []string{"default", "research"}},
		{name: "ringo", sessions: []string{"default"}},
	}
	for _, tc := range []struct{ width, columns int }{{100, 3}, {76, 3}, {75, 2}, {50, 2}, {49, 1}, {24, 1}} {
		raw := hiveHoneycomb(bees, tc.width, stylesFor(false), false)
		if strings.Contains(raw, "\x1b[48") || strings.Contains(raw, "session") || strings.Contains(raw, "│") {
			t.Fatal("Plan A contains fill, counts or vertical card walls")
		}
		var caps []string
		for _, line := range strings.Split(ansi.Strip(raw), "\n") {
			if strings.Contains(line, "─") && strings.TrimSpace(strings.ReplaceAll(line, "─", "")) == "" {
				caps = append(caps, line)
			}
		}
		rows := (len(bees) + tc.columns - 1) / tc.columns
		if len(caps) != 2*rows {
			t.Fatalf("width %d: cell borders are staggered", tc.width)
		}
		for row := 0; row < rows; row++ {
			count := min(tc.columns, len(bees)-row*tc.columns)
			expected := strings.Join(makeCaps(count), "  ")
			if caps[2*row] != expected || caps[2*row+1] != expected {
				t.Fatalf("width %d: edges not aligned", tc.width)
			}
		}
	}
}

func makeCaps(count int) []string {
	caps := make([]string, count)
	for i := range caps {
		caps[i] = "   " + strings.Repeat("─", 18) + "   "
	}
	return caps
}
