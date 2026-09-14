package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const hiveCellWidth = 24
const hiveCellGap = 2

// Fill rows from the left using whole cells. Every cell uses the same geometry,
// including incomplete rows and cells with longer names or more sessions.
func hiveHoneycomb(bees []hiveBee, width int, st panelStyles, stale bool) string {
	if len(bees) == 0 || width <= 0 {
		return ""
	}
	cellWidth := min(hiveCellWidth, width)
	columns := min(len(bees), max(1, (width+hiveCellGap)/(cellWidth+hiveCellGap)))
	contents := make([][]string, len(bees))
	height := 6
	for i, bee := range bees {
		contents[i] = hiveCellContent(bee, cellWidth, st, stale && !bee.local)
		height = max(height, len(contents[i])+2)
	}
	if height%2 != 0 {
		height++
	}
	cells := make([][]string, len(bees))
	for i, bee := range bees {
		cells[i] = drawHiveCell(bee, contents[i], cellWidth, height, st, stale && !bee.local)
	}
	var lines []string
	for start := 0; start < len(bees); start += columns {
		end := min(start+columns, len(bees))
		for row := 0; row < height+2; row++ {
			parts := make([]string, 0, end-start)
			for col := start; col < end; col++ {
				parts = append(parts, cells[col][row])
			}
			lines = append(lines, strings.Join(parts, strings.Repeat(" ", hiveCellGap)))
		}
		if end < len(bees) {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func hiveCellContent(bee hiveBee, width int, st panelStyles, stale bool) []string {
	var content []string
	textWidth := max(1, width-8)
	add := func(s string) { content = append(content, strings.Split(ansi.Hardwrap(s, textWidth, false), "\n")...) }
	dot := st.success.Render("●")
	if stale {
		dot = st.muted.Render("○")
	}
	name := dot + " " + st.text.Bold(true).Render(safePanel(bee.name))
	if bee.local {
		name += " " + st.accent.Render("YOU")
	}
	add(name)
	if stale {
		add(st.muted.Render("LAST KNOWN"))
	}
	for _, session := range bee.sessions {
		add(st.text.Render(safePanel(session)))
	}
	if len(bee.sessions) == 0 {
		add(st.muted.Render("No sessions"))
	}
	return content
}

func hiveCell(bee hiveBee, width int, st panelStyles, stale bool) []string {
	content := hiveCellContent(bee, width, st, stale)
	height := max(6, len(content)+2)
	if height%2 != 0 {
		height++
	}
	return drawHiveCell(bee, content, width, height, st, stale)
}

func drawHiveCell(bee hiveBee, content []string, width, height int, st panelStyles, stale bool) []string {
	border := st.muted
	if bee.local {
		border = st.accent
	}
	if stale {
		border = st.line
	}
	depth := min(3, max(0, (width-2)/2))
	cap := strings.Repeat(" ", depth) + border.Render(strings.Repeat("─", max(0, width-2*depth))) + strings.Repeat(" ", depth)
	rows := []string{cap}
	offset := (height - len(content)) / 2
	for i := 0; i < height; i++ {
		distance := min(i, height-1-i)
		inset := 0
		if height > 2 {
			inset = max(0, depth-1) * (height/2 - 1 - distance) / (height/2 - 1)
		}
		left, right := "╱", "╲"
		if i >= height/2 {
			left, right = "╲", "╱"
		}
		text := ""
		if i >= offset && i-offset < len(content) {
			text = content[i-offset]
		}
		padding := max(0, width-2*inset-2-lipgloss.Width(text))
		rows = append(rows, strings.Repeat(" ", inset)+border.Render(left)+strings.Repeat(" ", padding/2)+text+strings.Repeat(" ", padding-padding/2)+border.Render(right)+strings.Repeat(" ", inset))
	}
	return append(rows, cap)
}
