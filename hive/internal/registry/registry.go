// Package registry assigns stable public names without storing terminal content.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Registry struct {
	mu    sync.Mutex
	path  string
	Names map[string]string
}

func Open(path string) (*Registry, error) {
	r := &Registry{path: path, Names: map[string]string{}}
	b, e := os.ReadFile(path)
	if e == nil {
		e = json.Unmarshal(b, &r.Names)
	}
	if os.IsNotExist(e) {
		e = nil
	}
	if e == nil && r.Names == nil {
		e = fmt.Errorf("registry must be a JSON object, not null")
	}
	return r, e
}
func (r *Registry) Name(owner, wanted string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := wanted
	for n := 2; ; n++ {
		used := false
		for k, v := range r.Names {
			if k != owner && v == name {
				used = true
				break
			}
		}
		if !used {
			break
		}
		name = fmt.Sprintf("%s-%d", wanted, n)
	}
	if r.Names[owner] == name {
		return name, nil
	}
	old, exists := r.Names[owner]
	if !exists && len(r.Names) >= 1024 {
		return "", fmt.Errorf("device registry is full (1024 names)")
	}
	r.Names[owner] = name
	b, e := json.MarshalIndent(r.Names, "", "  ")
	if e == nil {
		e = os.MkdirAll(filepath.Dir(r.path), 0700)
	}
	if e == nil {
		var f *os.File
		f, e = os.CreateTemp(filepath.Dir(r.path), ".registry-*")
		if e == nil {
			defer os.Remove(f.Name())
			_, e = f.Write(b)
			if e == nil {
				e = f.Sync()
			}
			ce := f.Close()
			if e == nil {
				e = ce
			}
			if e == nil {
				e = os.Rename(f.Name(), r.path)
			}
		}
	}
	if e != nil {
		if exists {
			r.Names[owner] = old
		} else {
			delete(r.Names, owner)
		}
	}
	return name, e
}

// DiskBytes reports the bounded metadata file size, without reading its contents.
func (r *Registry) DiskBytes() int64 {
	info, e := os.Stat(r.path)
	if e != nil {
		return 0
	}
	return info.Size()
}
