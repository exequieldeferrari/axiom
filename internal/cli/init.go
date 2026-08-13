package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/project"
	"github.com/exequieldeferrari/axiom/internal/store"
)

type initOptions struct {
	global    bool
	dryRun    bool
	telemetry bool
	addr      string
}

// runInit installs Axiom's Claude Code hooks.
func runInit(args []string, stdout io.Writer) error {
	opts, err := parseInitFlags(args)
	if err != nil {
		return err
	}
	exePath, err := axiomPath()
	if err != nil {
		return err
	}
	return runInstall(opts, exePath, stdout)
}

func parseInitFlags(args []string) (initOptions, error) {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	// flag prints its own message; suppressing it keeps the output to one line.
	flags.SetOutput(io.Discard)

	var opts initOptions
	flags.BoolVar(&opts.global, "global", false, "install for all projects in ~/.claude/settings.json")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "print the resulting settings without writing them")
	flags.BoolVar(&opts.telemetry, "telemetry", false, "also export Claude Code's telemetry to a local axiom receiver")
	flags.StringVar(&opts.addr, "addr", DefaultAddr, "receiver address to configure with --telemetry")

	if err := flags.Parse(args); err != nil {
		return opts, &UsageError{Msg: err.Error()}
	}
	if flags.NArg() > 0 {
		return opts, &UsageError{Msg: fmt.Sprintf("unexpected argument %q", flags.Arg(0))}
	}
	return opts, nil
}

func runInstall(opts initOptions, exePath string, stdout io.Writer) error {
	settings, err := settingsPath(opts.global)
	if err != nil {
		return err
	}

	endpoint := ""
	if opts.telemetry {
		endpoint = claude.TelemetryEndpoint(opts.addr)
	}

	res, err := claude.InstallFile(settings, claude.InstallOptions{
		ExePath:           exePath,
		TelemetryEndpoint: endpoint,
		DryRun:            opts.dryRun,
	})
	if err != nil {
		var conflict *claude.ConflictError
		if errors.As(err, &conflict) {
			return fmt.Errorf("%w\n%s was not modified; remove the existing hook to reinstall", conflict, settings)
		}
		var telemetry *claude.TelemetryConflictError
		if errors.As(err, &telemetry) {
			return fmt.Errorf("%w\n%s was not modified; remove the existing variable to let axiom configure telemetry", telemetry, settings)
		}
		return err
	}

	switch {
	case opts.dryRun:
		fmt.Fprintf(stdout, "Would write %s\n", settings)
		fmt.Fprintf(stdout, "Hook command: %s hook claude\n", exePath)
		fmt.Fprintf(stdout, "Events: %s\n", strings.Join(claude.HookEvents, ", "))
		if opts.telemetry {
			fmt.Fprintf(stdout, "Telemetry:\n%s", claude.TelemetrySummary(endpoint))
		}
		fmt.Fprintf(stdout, "\n%s", res.Content)
	case !res.Changed:
		fmt.Fprintf(stdout, "Axiom is already installed in %s\n", settings)
	default:
		fmt.Fprintf(stdout, "Installed Axiom hooks in %s\n", settings)
		fmt.Fprintf(stdout, "Events: %s\n", strings.Join(claude.HookEvents, ", "))
		if dir, err := store.DefaultDir(); err == nil {
			fmt.Fprintf(stdout, "Recording to %s\n", filepath.Join(dir, store.EventsFile))
		}
		if opts.telemetry {
			fmt.Fprintf(stdout, "\nClaude Code will export telemetry to %s\n", endpoint)
			fmt.Fprint(stdout, "Run 'axiom observe' while you work to record it.\n")
		}
		// Claude Code loads settings once, at session start, so an install made
		// during a session does nothing until the next one. Without this, the
		// first thing a new user sees is a profile with no events in it.
		fmt.Fprint(stdout, "\nClaude Code reads its settings when a session starts,\n"+
			"so start a new session before expecting this to be active.\n")
		if !opts.global {
			fmt.Fprint(stdout, "\nNote: Claude Code only adds .claude/settings.local.json to your git excludes\n"+
				"when it writes that file itself. Add it to .gitignore if you do not want it committed.\n")
		}
	}
	return nil
}

// axiomPath resolves the binary Claude Code should run.
//
// The absolute path is used because a hook process does not necessarily
// inherit the PATH that installed Axiom.
func axiomPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the axiom binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// `go run` builds into a temporary directory, and an archive is often
	// unpacked into one. Either is deleted eventually, and a hook pointing
	// there breaks with it.
	if tmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		if strings.HasPrefix(exe, tmp+string(os.PathSeparator)) {
			return "", fmt.Errorf("axiom is running from a temporary directory (%s), which will be deleted;\n"+
				"move the binary somewhere permanent, such as /usr/local/bin, and run 'axiom init' from there.\n"+
				"In a source checkout, run 'make build' and use ./bin/axiom", exe)
		}
	}
	return exe, nil
}

// settingsPath chooses the Claude Code settings file to modify.
//
// The project default is settings.local.json rather than settings.json:
// settings.json is meant to be committed, which would enable Axiom for
// teammates who do not have the binary installed.
func settingsPath(global bool) (string, error) {
	if !global {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("determine the working directory: %w", err)
		}
		return filepath.Join(project.Root(cwd), ".claude", "settings.local.json"), nil
	}

	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine the home directory: %w", err)
		}
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "settings.json"), nil
}
