package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type panelStyles struct{ text, muted, accent, success, danger, line, selected, badge lipgloss.Style }

func stylesFor(dark bool) panelStyles {
	ink, muted, accent, green, red, line, selected := "#343C44", "#737A80", "#8A651F", "#34715C", "#B4483C", "#C9CDCB", "#E8E4D9"
	if dark {
		ink, muted, accent, green, red, line, selected = "#DADEE2", "#959DA6", "#DAB66E", "#79BC9C", "#EE9C8C", "#444B53", "#37362F"
	}
	style := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	return panelStyles{text: style(ink), muted: style(muted), accent: style(accent), success: style(green), danger: style(red), line: style(line), selected: style(ink).Background(lipgloss.Color(selected)), badge: lipgloss.NewStyle().Foreground(lipgloss.Color("#292B2C")).Background(lipgloss.Color("#E3BF73")).Bold(true)}
}
func panelFit(s string, width int) string { return ansi.Truncate(s, max(0, width), "") }
func (m panelModel) body() (string, []int) {
	st := stylesFor(m.dark)
	width := max(8, m.width-4)
	lines := []string{}
	rows := []int{}
	add := func(text string) { lines = append(lines, panelFit(text, width)) }
	if !m.ready {
		add(st.muted.Render("Loading local configuration…"))
		return strings.Join(lines, "\n"), rows
	}
	if m.page == 2 {
		return m.hiveBody(), rows
	}
	if m.page == 0 {
		add(st.text.Bold(true).Render("Connection"))
		add(st.muted.Render("Your agent stays on this machine."))
		add("")
		labels := []string{"Hive address", "Visible name", "SSH identity", "Enrollment token"}
		for i, label := range labels {
			rows = append(rows, len(lines))
			marker := "  "
			if m.focus == i {
				marker = "› "
			}
			add(st.muted.Render(marker + label))
			value := m.fields[i].View()
			if i != 3 && (!m.editing || m.focus != i) {
				value = safePanel(m.fields[i].Value())
				if value == "" {
					value = st.muted.Render(m.fields[i].Placeholder)
				} else {
					value = st.text.Render(value)
				}
			}
			field := lipgloss.NewStyle().Padding(0, 1).Width(width)
			if m.focus == i {
				field = st.selected.Padding(0, 1).Width(width)
			}
			add(field.Render(panelFit(value, width-2)))
			add("")
		}
		rows = append(rows, len(lines))
		label := " Save changes "
		if !m.dirty {
			label = " All changes saved "
		}
		if m.busy {
			label = " Working… "
		}
		if m.focus == 4 {
			add(st.badge.Render(label))
		} else {
			add(st.accent.Render(label))
		}
		add("")
		add(st.muted.Render("Private keys stay on this device."))
	} else {
		add(st.text.Bold(true).Render("Shared sessions"))
		add(st.muted.Render(fmt.Sprintf("%d selected · full access for Hive members", len(m.snapshot.config.Rules))))
		add("")
		if m.snapshot.sessionsErr != nil {
			add(st.danger.Render("Session discovery unavailable"))
			add(st.muted.Render(safePanel(m.snapshot.sessionsErr.Error())))
			add("")
		}
		selected := map[string]bool{}
		for _, r := range m.snapshot.config.Rules {
			selected[r.Session] = true
		}
		for i, s := range m.snapshot.sessions {
			rows = append(rows, len(lines))
			mark := "[ ]"
			if selected[s.Name] {
				mark = "[✓]"
			}
			state := "stopped"
			if s.Running {
				state = "running"
			}
			for _, share := range m.snapshot.status.Shares {
				if share.Label == s.Name {
					state = "published"
				}
			}
			nameWidth := max(1, width-len(state)-9)
			name := panelFit(safePanel(s.Name), nameWidth)
			row := fmt.Sprintf(" %s %s%s %s ", mark, name, strings.Repeat(" ", max(0, nameWidth-lipgloss.Width(name))), state)
			if m.focus == i {
				add(st.selected.Render(row))
			} else {
				add(st.text.Render(row))
			}
			add("")
		}
		if len(m.snapshot.sessions) == 0 {
			add(st.muted.Render("No running named sessions found."))
			add(st.muted.Render("Start a Herdr session, then select it here."))
			add("")
		}
		rows = append(rows, len(lines))
		label := " Start sharing "
		if m.snapshot.config.Enabled {
			label = " Pause sharing "
		}
		if m.busy {
			label = " Working… "
		}
		if m.focus == len(m.snapshot.sessions) {
			add(st.badge.Render(label))
		} else {
			add(st.accent.Render(label))
		}
		add("")
		add(st.muted.Render("Closing this pane keeps sharing enabled."))
	}
	return strings.Join(lines, "\n"), rows
}
func (m *panelModel) layout() {
	m.viewport.SetWidth(max(1, m.width-4))
	m.viewport.SetHeight(max(1, m.height-10))
	body, rows := m.body()
	m.viewport.SetContent(body)
	if m.focus != m.lastFocus || m.page != m.lastPage || m.height != m.lastHeight {
		if m.focus < len(rows) {
			row := rows[m.focus]
			if m.page == 0 && m.focus < 4 {
				row++
			}
			m.viewport.EnsureVisible(row, 0, 0)
		}
	}
	m.lastFocus, m.lastPage, m.lastHeight = m.focus, m.page, m.height
}
func (m panelModel) View() tea.View {
	st := stylesFor(m.dark)
	width := max(1, m.width-4)
	if m.width < 28 || m.height < 12 {
		v := tea.NewView(panelFit("Bee · enlarge pane (28 × 12)", m.width))
		v.AltScreen = true
		return v
	}
	status := "○  Sharing off"
	statusStyle := st.muted
	if m.snapshot.config.Enabled {
		status = "◌  Connecting"
		statusStyle = st.accent
	}
	if m.snapshot.status.Connected {
		status = "●  Connected"
		statusStyle = st.success
	}
	if m.snapshot.status.Error != "" {
		status = "!  Offline"
		statusStyle = st.danger
	}
	brand := st.badge.Render(" BEE ")
	header := brand + strings.Repeat(" ", max(1, width-lipgloss.Width(brand)-lipgloss.Width(status))) + statusStyle.Render(status)
	tabs := []string{" [1] Connection ", " [2] Sharing ", " [3] Hive "}
	if width < 42 {
		tabs = []string{"[1] Connect", "[2] Share", "[3] Hive"}
	}
	if width < 32 {
		tabs = []string{"1 Conn", "2 Share", "3 Hive"}
	}
	tabWidths := make([]int, len(tabs))
	for i, tab := range tabs {
		tabWidths[i] = lipgloss.Width(tab)
	}
	for i := range tabs {
		if m.page == i {
			tabs[i] = st.accent.Bold(true).Underline(true).Render(tabs[i])
		} else {
			tabs[i] = st.muted.Render(tabs[i])
		}
	}
	subtitle := safePanel(m.snapshot.config.Name)
	if subtitle == "" {
		subtitle = "Publish from this device"
	}
	top := []string{panelFit(header, width), st.muted.Render(panelFit(subtitle, width)), "", strings.Join(tabs, " "), st.line.Render(strings.Repeat("─", width))}
	notice := m.message
	noticeStyle := st.muted
	if m.messageError {
		noticeStyle = st.danger
	}
	if notice == "" && m.snapshot.status.Error != "" {
		notice = m.snapshot.status.Error
		noticeStyle = st.danger
	}
	if notice == "" {
		notice = "Live status · refreshes every 2s"
		if m.page == 2 {
			notice = "Hive sessions · auto-refresh"
		}
	}
	keys := "Tab move · Enter edit · s sharing · u update"
	if m.page == 1 {
		keys = "↑↓ move · Space select · s sharing · u update"
	}
	if m.page == 2 {
		keys = "↑↓ scroll · r refresh"
	}
	if m.editing {
		keys = "Enter done · Tab next field · Ctrl+S save"
	}
	footer := []string{st.line.Render(strings.Repeat("─", width)), noticeStyle.Render(panelFit(safePanel(notice), width)), st.muted.Render(panelFit(keys, width)), st.muted.Render(panelFit("1/2/3 sections · r refresh · q close", width))}
	if width < 36 {
		footer[3] = st.muted.Render("1/2/3 tabs · q close")
	}
	content := strings.Join(top, "\n") + "\n" + m.viewport.View() + "\n" + strings.Join(footer, "\n")
	content = lipgloss.NewStyle().Padding(0, 2).Render(content)
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	_, rows := m.body()
	offset := m.viewport.YOffset()
	v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		click, ok := msg.(tea.MouseClickMsg)
		if !ok || click.Button != tea.MouseLeft {
			return nil
		}
		x, y := click.X-2, click.Y
		target := -99
		if y == 3 {
			offset := 0
			for i, tabWidth := range tabWidths {
				if x >= offset && x < offset+tabWidth {
					target = []int{-1, -2, -5}[i]
					break
				}
				offset += tabWidth + 1
			}

		} else if y >= 5 && y < 5+m.viewport.Height() {
			row := y - 5 + offset
			for i, start := range rows {
				if row == start || (m.page == 0 && i < 4 && row == start+1) {
					target = i
					break
				}
			}
		}
		if target == -99 {
			return nil
		}
		clickMsg := panelClick{target: target, page: m.page}
		if target >= 0 && m.page == 1 {
			if target == len(m.snapshot.sessions) {
				clickMsg.target = -3
			} else {
				clickMsg.session = m.snapshot.sessions[target].Name
			}
		}
		return func() tea.Msg { return clickMsg }
	}
	return v
}
