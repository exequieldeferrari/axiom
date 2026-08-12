package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/store"
)

type uninstallOptions struct {
	global bool
	dryRun bool
}

// runUninstall removes Axiom's Claude Code configuration.
func runUninstall(args []string, stdout io.Writer) error {
	opts, err := parseUninstallFlags(args)
	if err != nil {
		return err
	}
	return runRemove(opts, stdout)
}

func parseUninstallFlags(args []string) (uninstallOptions, error) {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var opts uninstallOptions
	flags.BoolVar(&opts.global, "global", false, "remove the installation in ~/.claude/settings.json")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "print the resulting settings without writing them")

	if err := flags.Parse(args); err != nil {
		return opts, &UsageError{Msg: err.Error()}
	}
	if flags.NArg() > 0 {
		return opts, &UsageError{Msg: fmt.Sprintf("unexpected argument %q", flags.Arg(0))}
	}
	return opts, nil
}

// runRemove undoes what init wrote, in the same scope init would have written.
//
// Recorded events are deliberately left alone. Removing an integration says
// nothing about wanting to throw away the work it recorded, so the command
// reports where that data is and leaves deleting it to the user.
func runRemove(opts uninstallOptions, stdout io.Writer) error {
	settings, err := settingsPath(opts.global)
	if err != nil {
		return err
	}

	removal, err := claude.UninstallFile(settings, opts.dryRun)
	if err != nil {
		return err
	}
	if !removal.Changed() {
		fmt.Fprintf(stdout, "Axiom is not installed in %s\n", settings)
		return nil
	}

	verb := "Removed"
	if opts.dryRun {
		verb = "Would remove"
	}
	fmt.Fprintf(stdout, "%s Axiom from %s\n", verb, settings)
	if len(removal.Events) > 0 {
		fmt.Fprintf(stdout, "  %s: %s\n", plural(removal.Handlers, "hook"), strings.Join(removal.Events, ", "))
	}
	if removal.Telemetry {
		fmt.Fprint(stdout, "  telemetry configuration\n")
	}
	if removal.Empty {
		if opts.dryRun {
			fmt.Fprintf(stdout, "\n%s would hold no settings and could be deleted.\n", settings)
		} else {
			fmt.Fprintf(stdout, "\n%s holds no settings now and can be deleted.\n", settings)
		}
	}

	if opts.dryRun {
		fmt.Fprintf(stdout, "\n%s", removal.Content)
		return nil
	}
	if dir, err := store.DefaultDir(); err == nil {
		fmt.Fprintf(stdout, "\nRecorded events are left in %s\n", filepath.Join(dir, store.EventsFile))
		fmt.Fprintf(stdout, "Delete %s to remove them.\n", dir)
	}
	return nil
}
