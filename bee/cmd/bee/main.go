package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/deepshape-ai/herdr-hive/bee/internal/app"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	exe, e := os.Executable()
	if e == nil {
		exe, e = filepath.EvalSymlinks(exe)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := (app.App{Dir: config.Dir(), Out: os.Stdout, Version: version, Executable: exe}).Execute(ctx, os.Args[1:]); e != nil {
		if errors.Is(e, publisher.ErrRestart) {
			stop()
			e = syscall.Exec(exe, os.Args, os.Environ())
		}
		fmt.Fprintln(os.Stderr, "bee:", e)
		os.Exit(1)
	}
}
