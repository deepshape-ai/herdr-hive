package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/registry"
	"github.com/deepshape-ai/herdr-hive/hive/internal/server"
	"golang.org/x/crypto/ssh"
)

var version = "dev"
var executable string
var errRestart = errors.New("restart Hive process")

func main() {
	var e error
	executable, e = os.Executable()
	if e == nil {
		executable, e = filepath.EvalSymlinks(executable)
	}
	if e == nil {
		e = run()
	}
	if errors.Is(e, flag.ErrHelp) {
		return
	}
	if errors.Is(e, errRestart) {
		e = syscall.Exec(executable, os.Args, os.Environ())
	}
	if e != nil {
		slog.Error(e.Error())
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return nil
	}
	if len(os.Args) > 1 && os.Args[1] == "update" {
		return upgrade(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "inspect" {
		return inspect(os.Args[2:])
	}
	f := flag.NewFlagSet("hive", flag.ContinueOnError)
	maxConnections := f.Int("max-connections", 64, "maximum simultaneous SSH connections")
	maxChannels := f.Int("max-channels", 16, "maximum application channels across all consumers")
	listen := f.String("listen", "127.0.0.1:2222", "SSH listen address")
	state := f.String("state-dir", "", "private state directory (required)")
	keys := f.String("authorized-keys", "", "registered device public keys (required)")
	if e := f.Parse(os.Args[1:]); e != nil {
		return e
	}
	if *maxConnections < 1 || *maxConnections > 1024 || *maxChannels < 1 || *maxChannels > 256 {
		return fmt.Errorf("limits out of range: connections 1..1024, channels 1..256")
	}
	if *state == "" || *keys == "" || f.NArg() != 0 {
		return fmt.Errorf("usage: hive --state-dir PATH --authorized-keys PATH [--listen HOST:PORT]")
	}
	if e := os.MkdirAll(*state, 0700); e != nil {
		return e
	}
	fi, e := os.Stat(*state)
	if e != nil {
		return e
	}
	if fi.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state directory must have mode 0700")
	}
	// A process lock prevents concurrent writers and accidental host-key replacement.
	lock, e := os.OpenFile(filepath.Join(*state, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("state directory is already in use: %w", e)
	}
	keyPath := filepath.Join(*state, "host_key")
	b, e := os.ReadFile(keyPath)
	if os.IsNotExist(e) {
		_, private, er := ed25519.GenerateKey(rand.Reader)
		if er != nil {
			return er
		}
		block, er := ssh.MarshalPrivateKey(private, "Herdr Hive")
		if er != nil {
			return er
		}
		b = pem.EncodeToMemory(block)
		file, er := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if er != nil {
			return er
		}
		_, er = file.Write(b)
		ce := file.Close()
		if er != nil {
			return er
		}
		if ce != nil {
			return ce
		}
	} else if e != nil {
		return e
	}
	signer, e := ssh.ParsePrivateKey(b)
	if e != nil {
		return e
	}
	if _, e = os.ReadFile(*keys); e != nil {
		return e
	}
	r, e := registry.Open(filepath.Join(*state, "names.json"))
	if e != nil {
		return e
	}
	l, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	var restart atomic.Bool
	go func() {
		select {
		case sig := <-signals:
			restart.Store(sig == syscall.SIGHUP)
			stop()
		case <-ctx.Done():
		}
	}()
	slog.Info("Hive listening", "address", l.Addr(), "host_key", ssh.FingerprintSHA256(signer.PublicKey()), "version", version)
	gateway := server.New(r, *keys)
	gateway.Version = version
	gateway.Executable = executable
	inspectionDone := make(chan struct{})
	gateway.Limits(*maxConnections, *maxChannels)
	go func() {
		defer close(inspectionDone)
		if e := gateway.Inspect(ctx, filepath.Join(*state, "inspect.sock")); e != nil {
			slog.Error("inspection endpoint failed", "error", e)
			stop()
		}
	}()
	controlDone := make(chan struct{})
	go func() {
		defer close(controlDone)
		if err := server.Control(ctx, filepath.Join(*state, "control.sock"), func() { restart.Store(true); stop() }); err != nil {
			slog.Error("control endpoint failed", "error", err)
			stop()
		}
	}()
	e = gateway.Serve(ctx, l, signer)
	stop()
	<-inspectionDone
	<-controlDone
	if e == nil && restart.Load() {
		return errRestart
	}
	return e
}

func inspect(args []string) error {
	f := flag.NewFlagSet("inspect", flag.ContinueOnError)
	state := f.String("state-dir", "", "Hive state directory")
	asJSON := f.Bool("json", false, "JSON output")
	watch := f.Bool("watch", false, "refresh every two seconds")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *state == "" {
		return fmt.Errorf("--state-dir is required")
	}
	for {
		c, e := net.DialTimeout("unix", filepath.Join(*state, "inspect.sock"), time.Second)
		if e != nil {
			return e
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		var s server.Snapshot
		e = json.NewDecoder(c).Decode(&s)
		c.Close()
		if e != nil {
			return e
		}
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(s)
		} else {
			if *watch {
				fmt.Print("\x1b[2J\x1b[H")
			}
			fmt.Printf("Hive · %ds uptime\nSSH connections %d/%d · channels %d/%d · rejected %d\nGo heap %.1f MiB · runtime %.1f MiB · goroutines %d · registry %d B\n\n", s.UptimeSeconds, s.Connections, s.MaxConnections, s.Channels, s.MaxChannels, s.Rejected, float64(s.HeapBytes)/(1<<20), float64(s.RuntimeBytes)/(1<<20), s.Goroutines, s.RegistryBytes)
			for _, d := range s.Shares {
				fmt.Printf("%s  %s / %s  connections=%d  up=%d B  down=%d B\n", d.ID, d.Name, d.Session, d.Connections, d.ToPublisher, d.ToConsumer)
			}
		}
		if !*watch {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}
