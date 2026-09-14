package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Each Bee occupies a hexagonal cell. Alternating columns are offset,
// so the hive keeps its shape as the terminal changes between one and three columns.
func hiveHoneycomb(bees []hiveBee, width int, st panelStyles, stale bool) string {
	if len(bees) == 0 {
		return ""
	}
	columns := min(len(bees), max(1, min(3, (width+1)/23)))
	cellWidth := min(24, (width-(columns-1))/columns)
	gridWidth := columns*cellWidth + columns - 1
	margin := strings.Repeat(" ", max(0, (width-gridWidth)/2))
	var lines []string
	for start := 0; start < len(bees); start += columns {
		cells := make([][]string, columns)
		height := 0
		for col := 0; col < columns && start+col < len(bees); col++ {
			cell := hiveCell(bees[start+col], cellWidth, st, stale && !bees[start+col].local)
			if col%2 == 1 {
				cell = append([]string{"", ""}, cell...)
			}
			cells[col] = cell
			height = max(height, len(cell))
		}
		for row := 0; row < height; row++ {
			line := margin
			for col := 0; col < columns; col++ {
				text := ""
				if row < len(cells[col]) {
					text = cells[col][row]
				}
				line += text + strings.Repeat(" ", max(0, cellWidth-lipgloss.Width(text)))
				if col+1 < columns {
					line += " "
				}
			}
			lines = append(lines, line)
		}
		if start+columns < len(bees) {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func hiveCell(bee hiveBee, width int, st panelStyles, stale bool) []string {
	border := st.muted
	if bee.local {
		border = st.accent
	}
	if stale {
		border = st.line
	}
	textWidth := max(1, width-6)
	type cellLine struct {
		text  string
		style lipgloss.Style
	}
	var content []cellLine
	add := func(text string, style lipgloss.Style) {
		for _, line := range strings.Split(ansi.Hardwrap(text, textWidth, false), "\n") {
			content = append(content, cellLine{line, style})
		}
	}
	count := fmt.Sprintf("%d sessions", len(bee.sessions))
	if len(bee.sessions) == 1 {
		count = "1 session"
	}
	dot, state, stateStyle := "●", count, st.success
	if bee.local {
		state, stateStyle = "YOU · "+count, st.accent
	}
	if stale {
		dot, state, stateStyle = "○", "LAST KNOWN", st.muted
	}
	add(dot+" "+safePanel(bee.name), st.text.Bold(true))
	add(state, stateStyle)
	for _, session := range bee.sessions {
		add(safePanel(session), st.text)
	}
	if len(bee.sessions) == 0 {
		add("No sessions", st.muted)
	}
	// Short shoulders and straight sides keep the hexagon intact when names or
	// multiple sessions need more rows. The opaque interior protects contrast
	// in translucent terminals without changing any host or publisher colors.
	cap := "  " + border.Render(strings.Repeat("─", max(0, width-4))) + "  "
	rows := []string{cap}
	for i, line := range content {
		text := line.text
		inset, left, right := 0, "│", "│"
		if i == 0 {
			inset, left, right = 1, "╱", "╲"
		}
		inner := max(0, width-2*inset-2)
		padding := max(0, inner-lipgloss.Width(text))
		body := strings.Repeat(" ", padding/2) + text + strings.Repeat(" ", padding-padding/2)
		rows = append(rows, strings.Repeat(" ", inset)+border.Render(left)+line.style.Background(st.cell.GetBackground()).Render(body)+border.Render(right)+strings.Repeat(" ", inset))
	}
	rows = append(rows, " "+border.Render("╲")+st.cell.Render(strings.Repeat(" ", max(0, width-4)))+border.Render("╱")+" ", cap)
	return rows
}
