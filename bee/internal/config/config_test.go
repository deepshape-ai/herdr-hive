package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUnknownVersionCannotBeOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{"version":2,"name":"future","rules":[]}`)
	os.WriteFile(path, raw, 0600)
	if _, e := Update(dir, func(c *Config) error { c.Enabled = true; return nil }); e == nil {
		t.Fatal("accepted future config")
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(raw) {
		t.Fatal("overwrote unknown configuration")
	}
}
func TestConcurrentUpdatesPreserveRules(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := Update(dir, func(c *Config) error { r, e := NewRule(name); c.Rules = append(c.Rules, r); return e })
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	c, e := Load(dir)
	if e != nil || len(c.Rules) != 3 {
		t.Fatalf("%+v %v", c, e)
	}
}

func TestTrailingConfigIsPreservedOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{"version":1,"name":"Worker","rules":[]} {"version":2}`)
	os.WriteFile(path, raw, 0600)
	if _, e := Update(dir, func(c *Config) error { c.Name = "changed"; return nil }); e == nil {
		t.Fatal("accepted trailing configuration")
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(raw) {
		t.Fatal("overwrote unrecognized trailing data")
	}
}
