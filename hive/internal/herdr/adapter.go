// Package herdr recognizes native SSH bootstrap requests. It never executes shell text.
package herdr

import (
	"fmt"
	"io"
	"strings"
)

const (
	remoteOutputReadyMarker = "herdr-remote-output-ready:1"
	framedCommandPrefix     = "printf '\n%s\n' '" + remoteOutputReadyMarker + "'\n"
	framedOutputPrefix      = "\n" + remoteOutputReadyMarker + "\n"
)

type Action struct {
	Kind    string
	Output  string
	framing responseFraming
}

type responseFraming uint8

const (
	unframed responseFraming = iota
	framed
)

// BeginResponse writes protocol framing before any fixed or streamed response.
func (a Action) BeginResponse(w io.Writer) error {
	if a.framing != framed {
		return nil
	}
	_, err := io.WriteString(w, framedOutputPrefix)
	return err
}

// Resolve maps a supported Herdr request to a fixed publisher operation.
func Resolve(command, script string) (Action, error) {
	if command == "/bin/sh -s" {
		command = strings.TrimSpace(script)
	}
	framing := unframed
	if strings.HasPrefix(command, framedCommandPrefix) {
		command = strings.TrimPrefix(command, framedCommandPrefix)
		framing = framed
	}
	switch command {
	case "uname -s\nuname -m":
		return Action{Kind: "platform", framing: framing}, nil
	case "command -v herdr":
		return Action{Output: "/herdr\n", framing: framing}, nil
	case "test -x '/herdr'", "test -x /herdr":
		return Action{framing: framing}, nil
	case "test -x '/herdr' && '/herdr' status client --json", "test -x /herdr && /herdr status client --json":
		return Action{Kind: "status-client", framing: framing}, nil
	case "'/herdr' status server --json", "/herdr status server --json":
		return Action{Kind: "status-server", framing: framing}, nil
	case "exec '/herdr' remote-client-bridge", "exec /herdr remote-client-bridge":
		return Action{Kind: "client", framing: framing}, nil
	case "exec '/herdr' remote-client-bridge </dev/null", "exec /herdr remote-client-bridge </dev/null":
		return Action{Kind: "check", framing: framing}, nil
	}
	// Discovery scripts are data, not commands: validate their constrained shape and
	// advertised Herdr version, then return Hive's virtual binary without executing them.
	if supportedDiscoveryScript(command) {
		return Action{Output: "/herdr\n", framing: framing}, nil
	}
	return Action{}, fmt.Errorf("unsupported Herdr SSH request; use a tested Herdr version (see compatibility documentation)")
}
