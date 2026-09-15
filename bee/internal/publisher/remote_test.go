package publisher

import (
	"strings"
	"testing"
)

func TestResolveTargetIdentityAndCapabilities(t *testing.T) {
	shares := []Share{
		{ID: "s-a", Name: "Build/host", Label: "research", API: true, Generation: strings.Repeat("a", 32)},
		{ID: "s-b", Name: "Build", Label: "host/research", API: true, Generation: strings.Repeat("b", 32)},
		{ID: "s-old", Name: "Legacy", Label: "default"},
	}
	if _, err := ResolveTarget(shares, "Build/host/research"); err == nil {
		t.Fatal("ambiguous label accepted")
	}
	if got, err := ResolveTarget(shares, "s-a"); err != nil || got.ID != "s-a" {
		t.Fatal(got, err)
	}
	for _, name := range []string{"missing", "Legacy/default", "s-old", "Build"} {
		if _, err := ResolveTarget(shares, name); err == nil {
			t.Fatal("accepted", name)
		}
	}
}
