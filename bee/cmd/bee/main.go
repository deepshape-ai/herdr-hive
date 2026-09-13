package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/deepshape-ai/herdr-hive/bee/internal/app"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := (app.App{Dir: config.Dir(), Out: os.Stdout}).Execute(ctx, os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "bee:", e)
		os.Exit(1)
	}
}
