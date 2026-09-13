// Package herdr recognizes native SSH bootstrap requests. It never executes shell text.
package herdr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type Action struct {
	Kind   string
	Output string
}

// Resolve maps a supported Herdr request to a fixed publisher operation.
func Resolve(command, script string) (Action, error) {
	if command == "/bin/sh -s" {
		command = strings.TrimSpace(script)
	}
	switch command {
	case "uname -s\nuname -m":
		return Action{Kind: "platform"}, nil
	case "command -v herdr":
		return Action{Output: "/herdr\n"}, nil
	case "test -x '/herdr'", "test -x /herdr":
		return Action{}, nil
	case "test -x '/herdr' && '/herdr' status client --json", "test -x /herdr && /herdr status client --json":
		return Action{Kind: "status-client"}, nil
	case "'/herdr' status server --json", "/herdr status server --json":
		return Action{Kind: "status-server"}, nil
	case "exec '/herdr' remote-client-bridge", "exec /herdr remote-client-bridge":
		return Action{Kind: "client"}, nil
	case "exec '/herdr' remote-client-bridge </dev/null", "exec /herdr remote-client-bridge </dev/null":
		return Action{Kind: "check"}, nil
	}
	// Exact upstream discovery script hashes; no script is executed, even on a match.
	sum := sha256.Sum256([]byte(strings.TrimSpace(command)))
	if discoveryHashes[hex.EncodeToString(sum[:])] {
		return Action{Output: "/herdr\n"}, nil
	}
	return Action{}, fmt.Errorf("unsupported Herdr SSH request; use a tested Herdr version (see compatibility documentation)")
}

var discoveryHashes = map[string]bool{
	"4b011e8fe36cf5f34661293b011fde828c1d41a7b3c4117f2f124a5f30b05b91": true,
	"1326e9147f0d24a308960957f0d02e5c6e8e1c0129cb9130f0a2d20079254a94": true,
}
