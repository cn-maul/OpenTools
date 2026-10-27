package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"opentools/internal/appdata"
	"opentools/internal/appkit"
	"opentools/internal/tasks"
	"opentools/pkg/tool"
)

// runCLI returns handled=false for a normal desktop launch.
func runCLI(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "list":
		return true, listTools()
	case "run":
		return true, runTool(args[1:])
	case "jobs":
		return true, listJobs(args[1:])
	case "job":
		return true, showJob(args[1:])
	case "store":
		return true, showStore(args[1:])
	case "install":
		return true, installTool(args[1:])
	case "uninstall":
		return true, uninstallTool(args[1:])
	case "version", "--version", "-v":
		fmt.Println("opentools", version)
		return true, nil
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return true, nil
	default:
		// A bare path is a convenience for `opentools run`.
		if !strings.HasPrefix(args[0], "-") {
			printUsage(os.Stderr)
			return true, fmt.Errorf("unknown command %q", args[0])
		}
		return false, nil
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `OpenTools - one desktop shell, many composable tools.

Usage:
  opentools                          open the desktop window
  opentools list                     list installed tools
  opentools run <tool> [flags]       run a tool in the foreground
  opentools run <tool> --json        emit the final job as JSON
  opentools jobs [-limit 20]         list recent jobs
  opentools job <id>                 show a job as JSON
  opentools store [--refresh]        list downloadable and installed tools
  opentools install <tool>           download and install a tool
  opentools uninstall <tool>         remove an installed tool
  opentools version

Run "opentools run <tool> -h" for a tool's inputs.
`)
}

func openApp(ctx context.Context) (*appkit.App, func(), error) {
	dir, err := appdata.Dir()
	if err != nil {
		return nil, func() {}, err
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := appkit.Open(ctx, dir, logger)
	if err != nil {
		return nil, func() {}, err
	}
	return app, func() { _ = app.Close() }, nil
}

func listTools() error {
	ctx := context.Background()
	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(app.Service.Tools())
}

func listJobs(args []string) error {
	flags := flag.NewFlagSet("jobs", flag.ContinueOnError)
	limit := flags.Int("limit", 20, "maximum jobs to return")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	jobs, err := app.Service.List(ctx, *limit)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		fmt.Println("No jobs yet.")
		return nil
	}
	for _, job := range jobs {
		fmt.Printf("%s  %-14s  %-10s  %s\n", job.ID, job.ToolID, job.Status, jobSummary(job))
	}
	return nil
}

func jobSummary(job tasks.Job) string {
	switch {
	case job.Result != nil && job.Result.Summary != "":
		return job.Result.Summary
	case job.Error != "":
		return job.Error
	default:
		return job.CreatedAt.Local().Format("2006-01-02 15:04")
	}
}

func showJob(args []string) error {
	flags := flag.NewFlagSet("job", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: opentools job <id>")
	}
	ctx := context.Background()
	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	job, err := app.Service.Get(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(job)
}

func runTool(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: opentools run <tool> [flags]")
	}
	toolID := args[0]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	def, err := app.Service.Tool(toolID)
	if err != nil {
		return err
	}

	values, jsonOutput, err := parseToolFlags(def, args[1:])
	if err != nil {
		return err
	}
	// Text-oriented tools read piped input only when they explicitly expose a
	// source field. Other tools must not consume an unrelated stdin stream.
	if _, hasSource := def.Input.Properties["source"]; hasSource {
		if source, ok := readStdinIfPiped(); ok {
			if current, exists := values["source"]; !exists || current == "" {
				values["source"] = source
			}
		}
	}
	job, err := app.Service.Start(ctx, toolID, values)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "job %s started\n", job.ID)

	events, unsubscribe := app.Service.Subscribe()
	defer unsubscribe()
	printed := 0
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = app.Service.Cancel(context.Background(), job.ID)
			return context.Canceled
		case event, ok := <-events:
			if !ok {
				return errors.New("event stream closed")
			}
			if event.Job.ID != job.ID {
				continue
			}
			if !jsonOutput {
				printed = printLogs(event.Job.Logs, printed)
			}
			if !event.Job.Status.Active() {
				return printResult(event.Job, jsonOutput)
			}
		case <-ticker.C:
			current, err := app.Service.Get(ctx, job.ID)
			if err != nil {
				return err
			}
			if !jsonOutput {
				printed = printLogs(current.Logs, printed)
			}
			if !current.Status.Active() {
				return printResult(current, jsonOutput)
			}
		}
	}
}

func printLogs(logs []tasks.LogEntry, printed int) int {
	if printed < 0 || printed > len(logs) {
		printed = 0
	}
	for _, entry := range logs[printed:] {
		fmt.Fprintln(os.Stderr, entry.Message)
	}
	return len(logs)
}

func printResult(job tasks.Job, jsonOutput bool) error {
	if job.Status == tasks.StatusCanceled {
		fmt.Fprintln(os.Stderr, "canceled")
		return context.Canceled
	}
	if job.Status == tasks.StatusFailed {
		return errors.New(job.Error)
	}
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(job)
	}
	if job.Result == nil {
		fmt.Println("Done.")
		return nil
	}
	fmt.Println(job.Result.Summary)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(job.Result); err != nil {
		return err
	}
	return nil
}

// parseToolFlags maps a tool's supported schema subset to command-line flags.
// JSON is accepted for arrays, objects and any field whose type is not
// represented by a simple flag.
func parseToolFlags(def tool.Definition, args []string) (map[string]any, bool, error) {
	fs := flag.NewFlagSet("run "+def.ID, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s - %s\n\nUsage: opentools run %s [flags] [--key value]\n\nFlags:\n", def.Name, def.Summary, def.ID)
		fs.PrintDefaults()
	}
	type stringFlag struct {
		key   string
		value *string
	}
	var stringsList []stringFlag
	values := map[string]any{}
	for key, schema := range def.Input.Properties {
		switch schema.Type {
		case "string":
			defaultValue := ""
			if value, ok := schema.Default.(string); ok {
				defaultValue = value
			}
			ptr := fs.String(key, defaultValue, fieldUsage(schema))
			stringsList = append(stringsList, stringFlag{key: key, value: ptr})
		case "number":
			defaultValue := 0.0
			if value, ok := schema.Default.(float64); ok {
				defaultValue = value
			}
			ptr := fs.Float64(key, defaultValue, fieldUsage(schema))
			values[key] = ptr
		case "boolean":
			defaultValue := false
			if value, ok := schema.Default.(bool); ok {
				defaultValue = value
			}
			ptr := fs.Bool(key, defaultValue, fieldUsage(schema))
			values[key] = ptr
		}
	}
	jsonOutput := fs.Bool("json", false, "emit the final job as JSON")
	if err := fs.Parse(args); err != nil {
		return nil, false, err
	}
	if fs.NArg() > 0 {
		return nil, false, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	supplied := map[string]bool{}
	fs.Visit(func(item *flag.Flag) {
		if item.Name != "json" {
			supplied[item.Name] = true
		}
	})
	explicit := make(map[string]any, len(supplied))
	for _, item := range stringsList {
		if supplied[item.key] {
			explicit[item.key] = *item.value
		}
	}
	for key, value := range values {
		if !supplied[key] {
			continue
		}
		switch ptr := value.(type) {
		case *float64:
			explicit[key] = *ptr
		case *bool:
			explicit[key] = *ptr
		}
	}
	return explicit, *jsonOutput, nil
}

func fieldUsage(schema *tool.Schema) string {
	usage := schema.Title
	if schema.Description != "" {
		usage += ": " + schema.Description
	}
	if schema.Default != nil {
		usage += fmt.Sprintf(" (default %v)", schema.Default)
	}
	return usage
}

func readStdinIfPiped() (string, bool) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", false
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
	if err != nil {
		return "", false
	}
	return string(data), len(data) > 0
}

func showStore(args []string) error {
	flags := flag.NewFlagSet("store", flag.ContinueOnError)
	refresh := flags.Bool("refresh", false, "refresh remote catalogs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: opentools store [--refresh]")
	}
	ctx := context.Background()
	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	snapshot := app.Service.PluginStore()
	if *refresh {
		snapshot, err = app.Service.RefreshPluginStore(ctx)
		if err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

func installTool(args []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: opentools install <tool>")
	}
	ctx := context.Background()
	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	// A fresh process never has an in-memory catalog, even when another tool
	// is installed. Refresh before selecting the requested package.
	if _, err := app.Service.RefreshPluginStore(ctx); err != nil {
		return err
	}
	snapshot, err := app.Service.InstallPlugin(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s.\n", flags.Arg(0))
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

func uninstallTool(args []string) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: opentools uninstall <tool>")
	}
	ctx := context.Background()
	app, closeApp, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer closeApp()
	snapshot, err := app.Service.UninstallPlugin(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("Uninstalled %s.\n", flags.Arg(0))
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}
