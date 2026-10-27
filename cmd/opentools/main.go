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

	"opentools/internal/appdata"
	"opentools/internal/appkit"
	"opentools/internal/desktop"
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
	dir, err := appdata.Dir()
	if err != nil {
		fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	app, err := appkit.Open(ctx, dir, logger)
	if err != nil {
		fatal(err)
	}
	defer app.Close()

	mygo.BindAs("Jobs", desktop.NewJobs(app.Service, version))
	go desktop.StartEventForwarder(ctx, app.Service)

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:             "OpenTools",
			Width:             1240,
			Height:            800,
			MinWidth:          820,
			MinHeight:         560,
			TitleBarStyle:     mygo.TitleBarHidden,
			TitleBarHeight:    48,
			HideWindowButtons: true,
			BackgroundColor:   "light-dark(#f5f7fa, #17181a)",
			URL:               "/",
		})
	})
	if runtime.GOOS == "darwin" {
		mygo.App.OnActivate(func(hasVisible bool) {
			if !hasVisible {
				mygo.NewWindow(mygo.WindowOptions{Title: "OpenTools", URL: "/"})
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
