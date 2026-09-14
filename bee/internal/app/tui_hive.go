package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

type hiveTarget struct{ address, identity, knownHosts string }
type panelHive struct {
	target hiveTarget
	seq    int
	shares []publisher.Share
	err    error
}
type hiveDirectory struct {
	panelHive
	loading, loaded bool
	next            time.Time
}

func (m *panelModel) refreshHive(force bool) tea.Cmd {
	c := m.snapshot.config
	target := hiveTarget{c.Hive, c.IdentityFile, c.KnownHosts}
	if target != m.hive.target {
		m.hive = hiveDirectory{panelHive: panelHive{target: target, seq: m.hive.seq + 1}}
	}
	if m.page != 2 || !m.ready || m.busy || m.hive.loading || target.address == "" || target.identity == "" || target.knownHosts == "" || (!force && time.Now().Before(m.hive.next)) {
		return nil
	}
	m.hive.loading = true
	m.hive.seq++
	seq := m.hive.seq
	return func() tea.Msg {
		shares, err := m.readHive(m.ctx, c)
		return panelHive{target: target, seq: seq, shares: shares, err: err}
	}
}

type hiveBee struct {
	name     string
	local    bool
	sessions []string
}

func (m panelModel) hiveBees() []hiveBee {
	var bees []hiveBee
	if m.snapshot.status.Connected {
		local := hiveBee{name: m.snapshot.config.Name, local: true}
		for _, s := range m.snapshot.status.Shares {
			if s.Name != "" {
				local.name = s.Name
			}
			local.sessions = append(local.sessions, s.Label)
		}
		if local.name == "" {
			local.name = "This device"
		}
		sort.Strings(local.sessions)
		bees = append(bees, local)
	}
	byName := map[string][]string{}
	for _, s := range m.hive.shares {
		byName[s.Name] = append(byName[s.Name], s.Label)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sort.Strings(byName[name])
		bees = append(bees, hiveBee{name: name, sessions: byName[name]})
	}
	return bees
}

func (m panelModel) hiveBody() string {
	st := stylesFor(m.dark)
	width := max(8, m.width-4)
	var lines []string
	add := func(s string) { lines = append(lines, panelFit(s, width)) }
	add(st.text.Bold(true).Render("Bees in this Hive"))
	add(st.muted.Render(safePanel(m.snapshot.config.Hive)))
	add("")
	if m.hive.target.address == "" || m.hive.target.identity == "" || m.hive.target.knownHosts == "" {
		add(st.muted.Render("Set up Connection to view Bees."))
		return strings.Join(lines, "\n")
	}
	bees := m.hiveBees()
	count := 0
	for _, bee := range bees {
		count += len(bee.sessions)
	}
	switch {
	case m.hive.err != nil:
		add(st.danger.Render("Hive list unavailable · r to retry"))
		if m.hive.loaded {
			add(st.muted.Render("Last known remote sessions shown below."))
		}
	case !m.hive.loaded:
		add(st.muted.Render("Loading connected Bees…"))
	default:
		add(st.muted.Render(fmt.Sprintf("%d Bees · %d shared sessions", len(bees), count)))
	}
	add("")
	for _, bee := range bees {
		name := safePanel(bee.name)
		if bee.local {
			name = panelFit(name, max(1, width-10)) + " (you)"
		}
		mark := st.success.Render("● ")
		if !bee.local && m.hive.err != nil {
			mark = st.muted.Render("○ ")
		}
		add(mark + st.text.Bold(true).Render(name))
		for i, session := range bee.sessions {
			branch := "  ├ "
			if i == len(bee.sessions)-1 {
				branch = "  └ "
			}
			add(st.muted.Render(branch) + st.text.Render(safePanel(session)))
		}
		add("")
	}
	if m.hive.loaded && m.hive.err == nil && len(bees) == 0 {
		add(st.muted.Render("No Bees are sharing sessions yet."))
		add("")
	}
	add(st.muted.Render("Only published sessions appear here."))
	return strings.Join(lines, "\n")
}

// Keep the transport injectable so panel state tests never contact a real Hive.
type hiveReader func(context.Context, config.Config) ([]publisher.Share, error)
