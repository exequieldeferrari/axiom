// Package cli implements the axiom command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/exequieldeferrari/axiom/internal/version"
)

// ExitCodeUsage is returned for usage errors such as unknown commands.
const ExitCodeUsage = 2

// UsageError indicates invalid command-line usage.
type UsageError struct {
	Msg string
}

func (e *UsageError) Error() string {
	return e.Msg
}

// IsUsage reports whether err is a usage error.
func IsUsage(err error) bool {
	var usage *UsageError
	return errors.As(err, &usage)
}

// Run dispatches axiom CLI commands.
// args should be os.Args (program name included).
func Run(args []string, stdout, stderr io.Writer) error {
	cmd := ""
	if len(args) > 1 {
		cmd = args[1]
	}

	switch cmd {
	case "", "help", "-h", "--help":
		printRootHelp(stdout)
		return nil
	case "version", "-v", "--version":
		_, err := fmt.Fprintf(stdout, "axiom %s\n", version.Version)
		return err
	case "init":
		return reportUsage(stderr, runInit(args[2:], stdout))
	case "hook":
		return reportUsage(stderr, runHook(args[2:]))
	case "profile":
		return reportUsage(stderr, runProfile(args[2:], stdout))
	default:
		err := &UsageError{Msg: fmt.Sprintf("unknown command %q", cmd)}
		fmt.Fprintf(stderr, "axiom: %v\n\n", err)
		printRootHelp(stderr)
		return err
	}
}

// reportUsage prints subcommand usage errors without dumping the root help,
// which would bury a one-line mistake.
func reportUsage(stderr io.Writer, err error) error {
	if IsUsage(err) {
		fmt.Fprintf(stderr, "axiom: %v\n", err)
	}
	return err
}

func printRootHelp(w io.Writer) {
	const help = `Axiom is a profiler for AI coding agents.

Usage:
  axiom <command>

Commands:
  init        Install the Claude Code integration
  profile     Analyze recorded events and report redundant work
  hook        Record an agent event (invoked by agent hooks, not by hand)
  help        Show this help message
  version     Print the axiom version

Run 'axiom help' for more information.
`
	fmt.Fprint(w, help)
}
