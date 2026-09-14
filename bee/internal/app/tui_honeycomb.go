package app

import (
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
	columns := min(len(bees), max(1, min(3, (width+1)/25)))
	cellWidth := min(30, (width-(columns-1))/columns)
	gridWidth := columns*cellWidth + columns - 1
	margin := strings.Repeat(" ", max(0, (width-gridWidth)/2))
	var lines []string
	for start := 0; start < len(bees); start += columns {
		cells := make([][]string, columns)
		height := 0
		for col := 0; col < columns && start+col < len(bees); col++ {
			cell := hiveCell(bees[start+col], cellWidth, st, stale && !bees[start+col].local)
			if col%2 == 1 {
				cell = append([]string{"", "", ""}, cell...)
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
	inset := 3
	textWidth := max(1, width-2*inset-4)
	var content []string
	add := func(s string) { content = append(content, strings.Split(ansi.Hardwrap(s, textWidth, false), "\n")...) }
	add(st.text.Bold(true).Render(safePanel(bee.name)))
	state := "● CONNECTED"
	stateStyle := st.success
	if bee.local {
		state = "● YOU"
		stateStyle = st.accent
	}
	if stale {
		state = "○ LAST KNOWN"
		stateStyle = st.muted
	}
	add(stateStyle.Render(state))
	add("")
	for _, session := range bee.sessions {
		add(st.text.Render(safePanel(session)))
	}
	if len(bee.sessions) == 0 {
		add(st.muted.Render("No sessions"))
	}
	// One breathing row at each end keeps names away from the sloping edges.
	content = append([]string{""}, append(content, "")...)
	rows := make([]string, 0, len(content)+2)
	cap := strings.Repeat(" ", inset) + border.Render(strings.Repeat("─", width-2*inset)) + strings.Repeat(" ", inset)
	rows = append(rows, cap)
	for i, text := range content {
		distance := min(i, len(content)-1-i)
		indent := max(0, inset-1-distance)
		left, right := "╱", "╲"
		if i*2 >= len(content) {
			left, right = "╲", "╱"
		}
		inner := width - 2*indent - 2
		padding := max(0, inner-lipgloss.Width(text))
		rows = append(rows, strings.Repeat(" ", indent)+border.Render(left)+strings.Repeat(" ", padding/2)+text+strings.Repeat(" ", padding-padding/2)+border.Render(right)+strings.Repeat(" ", indent))
	}
	return append(rows, cap)
}
