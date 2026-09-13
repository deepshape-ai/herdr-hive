package app

import (
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"testing"
)

func TestMissingSelectionsRemainRemovable(t *testing.T) {
	choices := sessionChoices([]herdr.Session{{Name: "live", Running: true}}, []config.Rule{{Session: "missing"}, {Session: "live"}})
	if len(choices) != 2 || choices[1].Name != "missing" || choices[1].Running {
		t.Fatalf("missing saved session is not available to unshare: %+v", choices)
	}
}
