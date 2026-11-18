// Command opentools is the OpenTools desktop app and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"

	"github.com/egoist/mygo"

	"github.com/cn-maul/OpenTools/internal/appdata"
	"github.com/cn-maul/OpenTools/internal/appkit"
	"github.com/cn-maul/OpenTools/internal/desktop"
)

const version = "0.3.3"

func main() {
	if handled, err := runCLI(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "opentools:", err)
			if errors.Is(err, context.Canceled) {
				os.Exit(130)
			}
			os.Exit(1)
		}
		return
	}
	runDesktop()
}

func runDesktop() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	mygo.App.SetName("OpenTools")
	mygo.App.SetVersion(version)
	// A second launch should surface the running shell (and its running
	// jobs) instead of starting a competing instance against the same
	// SQLite file and job directories.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	dir, err := appdata.Dir()
	if err != nil {
		fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	app, err := appkit.Open(ctx, dir, logger, desktop.NativeUI{})
	if err != nil {
		fatal(err)
	}
	defer app.Close()

	shell := desktop.NewShell(app.Service, version, dir)
	mygo.App.OnSecondInstance(func([]string, string) {
		shell.Open()
	})
	mygo.App.WhenReady(func() {
		shell.Open()
	})
	if runtime.GOOS == "darwin" {
		mygo.App.OnActivate(func(hasVisible bool) {
			if !hasVisible {
				shell.Open()
			}
		})
	}
	if err := mygo.App.Run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	fmt.Fprintln(os.Stderr, "opentools:", err)
	os.Exit(1)
}
