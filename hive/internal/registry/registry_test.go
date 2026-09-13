package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStableNamesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	r, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ owner, want string }{{"a", "Worker"}, {"b", "Worker-2"}, {"c", "Worker-3"}} {
		name, e := r.Name(tc.owner, "Worker")
		if e != nil || name != tc.want {
			t.Fatalf("%s %v", name, e)
		}
	}
	r, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if name, e := r.Name("b", "Worker"); e != nil || name != "Worker-2" {
		t.Fatalf("%s %v", name, e)
	}
}

func TestNullRegistryRejectedBeforeServing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	if e := os.WriteFile(path, []byte("null"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(path); e == nil {
		t.Fatal("accepted null registry")
	}
}
