// Command prdesc is the deterministic policy tool for pull request descriptions.
//
// It decides whether a body may be replaced, prepares an untrusted-data prompt
// for the model, and sanitizes model output. The model never makes those
// decisions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const (
	exitIneligible = 1
	exitUsage      = 2
)

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "prdesc: %v\n", err)
	var ee *exitError
	if errors.As(err, &ee) {
		os.Exit(ee.code)
	}
	os.Exit(exitUsage)
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return &exitError{code: exitUsage, msg: "missing command"}
	}
	switch args[0] {
	case "eligible":
		return runEligible(args[1:], stderr)
	case "prepare":
		return runPrepare(args[1:])
	case "sanitize":
		return runSanitize(args[1:])
	case "json-body":
		return runJSONBody(args[1:], stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return &exitError{code: exitUsage, msg: fmt.Sprintf("unknown command %q", args[0])}
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  prdesc eligible --template FILE --body FILE
  prdesc prepare --task FILE --title FILE --diff FILE --out FILE
  prdesc sanitize --template FILE --in FILE --out FILE
  prdesc json-body --in FILE
`)
}

func runEligible(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("eligible", flag.ContinueOnError)
	fs.SetOutput(stderr)
	templatePath := fs.String("template", "", "path to pull_request_template.md")
	bodyPath := fs.String("body", "", "path to the current pull request body")
	if err := fs.Parse(args); err != nil {
		return &exitError{code: exitUsage, msg: err.Error()}
	}
	if *templatePath == "" || *bodyPath == "" {
		return &exitError{code: exitUsage, msg: "eligible requires --template and --body"}
	}
	template, err := os.ReadFile(*templatePath)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(*bodyPath)
	if err != nil {
		return err
	}
	if !Eligible(string(body), string(template)) {
		fmt.Fprintln(stderr, "body is not empty or template-blank")
		return &exitError{code: exitIneligible, msg: "ineligible"}
	}
	return nil
}

func runPrepare(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	taskPath := fs.String("task", "", "path to the trusted task prompt")
	titlePath := fs.String("title", "", "path to the pull request title")
	diffPath := fs.String("diff", "", "path to the pull request diff")
	outPath := fs.String("out", "", "path to write the user prompt")
	if err := fs.Parse(args); err != nil {
		return &exitError{code: exitUsage, msg: err.Error()}
	}
	if *taskPath == "" || *titlePath == "" || *diffPath == "" || *outPath == "" {
		return &exitError{code: exitUsage, msg: "prepare requires --task, --title, --diff, and --out"}
	}
	task, err := os.ReadFile(*taskPath)
	if err != nil {
		return err
	}
	title, err := os.ReadFile(*titlePath)
	if err != nil {
		return err
	}
	diff, err := os.ReadFile(*diffPath)
	if err != nil {
		return err
	}
	return os.WriteFile(*outPath, []byte(Prepare(string(task), string(title), string(diff))), 0o644)
}

func runSanitize(args []string) error {
	fs := flag.NewFlagSet("sanitize", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	templatePath := fs.String("template", "", "path to pull_request_template.md")
	inPath := fs.String("in", "", "path to the model output")
	outPath := fs.String("out", "", "path to write the sanitized body")
	if err := fs.Parse(args); err != nil {
		return &exitError{code: exitUsage, msg: err.Error()}
	}
	if *templatePath == "" || *inPath == "" || *outPath == "" {
		return &exitError{code: exitUsage, msg: "sanitize requires --template, --in, and --out"}
	}
	template, err := os.ReadFile(*templatePath)
	if err != nil {
		return err
	}
	in, err := os.ReadFile(*inPath)
	if err != nil {
		return err
	}
	out, err := Sanitize(string(in), string(template))
	if err != nil {
		return &exitError{code: exitIneligible, msg: err.Error()}
	}
	return os.WriteFile(*outPath, []byte(out), 0o644)
}

func runJSONBody(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("json-body", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	inPath := fs.String("in", "", "path to the Markdown body")
	if err := fs.Parse(args); err != nil {
		return &exitError{code: exitUsage, msg: err.Error()}
	}
	if *inPath == "" {
		return &exitError{code: exitUsage, msg: "json-body requires --in"}
	}
	in, err := os.ReadFile(*inPath)
	if err != nil {
		return err
	}
	payload, err := EncodeBodyJSON(string(in))
	if err != nil {
		return err
	}
	_, err = stdout.Write(payload)
	return err
}
