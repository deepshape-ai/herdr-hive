package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

// Directory lists other devices' published sessions using the enrolled identity
// and the same verified host key as the publisher. Hive hides our own shares.
func Directory(ctx context.Context, c config.Config) ([]Share, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, cleanup, err := dial(ctx, c, "hive", "SSH-2.0-HerdrBeeDirectory")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var out directoryBuffer
	session.Stdout, session.Stderr = &out, io.Discard
	if err := session.Run("list --json"); err != nil {
		return nil, err
	}
	var shares []Share
	if err := json.Unmarshal(out.buffer.Bytes(), &shares); err != nil {
		return nil, errors.New("invalid Hive session list")
	}
	for _, s := range shares {
		if s.ID == "" || s.Name == "" || s.Label == "" {
			return nil, errors.New("incomplete Hive session list")
		}
	}
	return shares, nil
}

type directoryBuffer struct{ buffer bytes.Buffer }

func (b *directoryBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1<<20 {
		return 0, errors.New("Hive session list exceeds 1 MiB")
	}
	return b.buffer.Write(p)
}
