package herdr

import (
	"os"
	"testing"
	"time"
)

func TestBoundCloseIsSharedAndRejectsFurtherUse(t *testing.T) {
	b, _ := apiFixture(t)
	if err := b.Check(); err != nil {
		t.Fatal(err)
	}
	copy := b
	copy.Close()
	b.Close()
	if err := b.Check(); err == nil {
		t.Fatal("closed binding accepted")
	}
	if conn, err := b.Dial(); err == nil {
		conn.Close()
		t.Fatal("closed binding connected")
	}
}

func TestBoundRejectsChangedEndpointModTime(t *testing.T) {
	b, _ := apiFixture(t)
	changed := b.apiInfo.ModTime().Add(time.Second)
	if err := os.Chtimes(b.Socket, changed, changed); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(); err == nil {
		t.Fatal("changed endpoint modification time accepted")
	}
}
