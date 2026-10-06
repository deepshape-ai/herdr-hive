package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Reserve wire namespaces so upstream acknowledgements and late ordinary
// responses can never be mistaken for a reused downstream operation ID.
// Internal transitions and ordinary operations have separate bounded budgets.
const interestPrefix = "hive:surface:"
const requestPrefix = "hive:request:"

func (g *session) syncInterest() error {
	// Relinquish the previous source before activating its replacement.
	for _, b := range g.sources {
		if b.surfaceActive && (b != g.active || !g.surfaceActive) {
			if err := g.setInterest(b, false); err != nil {
				return err
			}
			if b.surfaceActive {
				// No slot yet: do not acquire a second source's layout authority.
				return nil
			}
		}
	}
	if b := g.active; b != nil && g.surfaceActive && !b.surfaceActive && b.boot != "" && b.stream != nil {
		return g.setInterest(b, true)
	}
	return nil
}

func (g *session) setInterest(b *backend, active bool) error {
	if !b.methods["client_shell.surface.set"] {
		return errors.New("publisher does not support surface interest")
	}
	if len(g.pending)-len(g.requestIDs) >= maxInternalRequests {
		// Retain only the latest desired state in g.active/g.surfaceActive.
		// An acknowledgement frees a slot and resumes this unsent transition.
		return nil
	}
	// Set the viewer's latest dimensions while this subscription is still
	// inactive; activation then uses the correct size, including after reconnect.
	if active && len(g.resize) > 0 {
		if err := g.send(b, g.resize); err != nil {
			return err
		}
		if g.sources[b.source.ID] != b {
			return nil
		}
	}
	g.interestSequence++
	id := fmt.Sprintf("%s%d", interestPrefix, g.interestSequence)
	v, _ := json.Marshal(map[string]any{"id": id, "method": "client_shell.surface.set", "params": map[string]any{"active": active}})
	p := append(append([]byte{15}, str(b.boot)...), str(string(v))...)
	g.pending[id] = b
	g.deadlines[id] = time.Now().Add(operationTimeout)
	b.surfaceActive = active
	return g.send(b, p)
}
