package app

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m panelModel) joinBody() (string, []int) {
	st := stylesFor(m.dark)
	width := max(8, m.width-4)
	lines, rows := []string{}, []int{}
	add := func(s string) { lines = append(lines, panelFit(s, width)) }
	add(st.text.Bold(true).Render("Join Hive"))
	if m.snapshot.config.Hive != "" {
		add(st.muted.Render(safePanel(m.snapshot.config.Hive)))
	} else {
		add(st.muted.Render("Paste an invitation from your administrator."))
	}
	add("")
	for i, label := range []string{"Invitation", "Visible name"} {
		rows = append(rows, len(lines))
		marker := "  "
		if m.focus == i {
			marker = "› "
		}
		add(st.muted.Render(marker + label))
		index := []int{4, 1}[i]
		value := m.fields[index].View()
		if index == 1 && (!m.editing || m.focus != i) {
			value = st.text.Render(safePanel(m.fields[index].Value()))
		}
		style := lipgloss.NewStyle().Padding(0, 1).Width(width)
		if m.focus == i {
			style = st.selected.Padding(0, 1).Width(width)
		}
		add(style.Render(panelFit(value, width-2)))
		add("")
	}
	label := " Join Hive "
	if m.snapshot.config.Hive != "" && m.fields[4].Value() == "" {
		label = " Finish connection "
		if m.snapshot.joined {
			label = " Reconnect Hive "
		}
	}
	if m.busy {
		label = " Connecting… "
	}
	rows = append(rows, len(lines))
	if m.focus == 2 {
		add(st.badge.Render(label))
	} else {
		add(st.accent.Render(label))
	}
	add("")
	if m.snapshot.joined {
		add(st.success.Render("Joined · Hive is in the Herdr sidebar."))
	} else if m.snapshot.config.Hive != "" {
		add(st.muted.Render("Connection saved. No invitation needed to retry."))
	}
	add(st.muted.Render("Choose sessions in Sharing to share your work."))
	add("")
	rows = append(rows, len(lines))
	label = " Advanced settings "
	if m.focus == 3 {
		add(st.selected.Render(label))
	} else {
		add(st.muted.Render(label))
	}
	return strings.Join(lines, "\n"), rows
}
