//go:build !linux

package herdr

import "os"

// macOS cannot open a socket vnode with O_EVTONLY. Retain its file identity
// and immutable modification time; socket traffic does not change this time.
func pinEndpoint(path string) (os.FileInfo, *os.File, error) {
	info, err := os.Stat(path)
	return info, nil, err
}
