package cli_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/cli"
)

func TestRun_NoArgs_PrintsHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := cli.Run([]string{"axiom"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"Axiom is a profiler for AI coding agents",
		"Usage:",
		"axiom <command>",
		"init",
		"profile",
		"hook",
		"help",
		"version",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRun_Help_PrintsSameAsNoArgs(t *testing.T) {
	t.Parallel()

	var noArgs, helpCmd bytes.Buffer
	if err := cli.Run([]string{"axiom"}, &noArgs, io.Discard); err != nil {
		t.Fatalf("no-args Run() error: %v", err)
	}
	if err := cli.Run([]string{"axiom", "help"}, &helpCmd, io.Discard); err != nil {
		t.Fatalf("help Run() error: %v", err)
	}
	if noArgs.String() != helpCmd.String() {
		t.Fatalf("help output mismatch\nno-args:\n%s\nhelp:\n%s", noArgs.String(), helpCmd.String())
	}
}

func TestRun_HelpFlags(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"axiom", "-h"},
		{"axiom", "--help"},
	} {
		var stdout bytes.Buffer
		err := cli.Run(args, &stdout, io.Discard)
		if err != nil {
			t.Fatalf("Run(%v) unexpected error: %v", args, err)
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Fatalf("Run(%v) missing help output:\n%s", args, stdout.String())
		}
	}
}

func TestRun_Version(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"axiom", "version"},
		{"axiom", "-v"},
		{"axiom", "--version"},
	} {
		var stdout, stderr bytes.Buffer
		err := cli.Run(args, &stdout, &stderr)
		if err != nil {
			t.Fatalf("Run(%v) unexpected error: %v", args, err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%v) stderr = %q, want empty", args, stderr.String())
		}
		if stdout.String() != "axiom dev\n" {
			t.Fatalf("Run(%v) stdout = %q, want %q", args, stdout.String(), "axiom dev\n")
		}
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := cli.Run([]string{"axiom", "nope"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run() error = nil, want usage error")
	}
	if !cli.IsUsage(err) {
		t.Fatalf("IsUsage(%v) = false, want true", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Fatalf("stderr missing unknown-command message:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr missing help:\n%s", stderr.String())
	}
}
