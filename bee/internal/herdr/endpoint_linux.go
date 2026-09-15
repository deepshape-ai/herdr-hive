package herdr

import (
	"os"

	"golang.org/x/sys/unix"
)

// O_PATH retains the socket's filesystem inode without connecting to Herdr.
// Linux can otherwise recycle its device/inode immediately after unlink, even
// while the publisher still holds the old os.FileInfo snapshot.
func pinEndpoint(path string) (os.FileInfo, *os.File, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	pin := os.NewFile(uintptr(fd), path)
	info, err := pin.Stat()
	if err != nil {
		pin.Close()
		return nil, nil, err
	}
	return info, pin, nil
}
