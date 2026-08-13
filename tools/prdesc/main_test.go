package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizedBodyIsNotEligible(t *testing.T) {
	t.Parallel()
	tmpl := repoTemplate(t)
	got, err := Sanitize(validModelBody(), tmpl)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if Eligible(got, tmpl) {
		t.Fatal("a generated description must not be eligible for a later overwrite")
	}
}

func TestRunEligibleExitCodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmpl := repoTemplate(t)
	tmplPath := filepath.Join(dir, "template.md")
	if err := os.WriteFile(tmplPath, []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}

	emptyPath := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(emptyPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"eligible", "--template", tmplPath, "--body", emptyPath}, io.Discard, io.Discard); err != nil {
		t.Fatalf("empty body should be eligible: %v", err)
	}

	humanPath := filepath.Join(dir, "human.md")
	if err := os.WriteFile(humanPath, []byte("## Summary\n\nA real change.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"eligible", "--template", tmplPath, "--body", humanPath}, io.Discard, io.Discard)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitIneligible {
		t.Fatalf("human body: got %v, want exit %d", err, exitIneligible)
	}
}

func TestRunJSONBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := filepath.Join(dir, "body.md")
	if err := os.WriteFile(in, []byte("## Summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := run([]string{"json-body", "--in", in}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"body":`) {
		t.Fatalf("unexpected payload %s", out.String())
	}
}

func TestRunPrepareAndSanitize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "template.md")
	if err := os.WriteFile(tmplPath, []byte(repoTemplate(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(dir, "task.txt")
	if err := os.WriteFile(taskPath, []byte("Write a description."), 0o644); err != nil {
		t.Fatal(err)
	}
	titlePath := filepath.Join(dir, "title.txt")
	if err := os.WriteFile(titlePath, []byte("feat: record provenance"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(dir, "pr.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/gone.go b/gone.go\ndeleted file mode 100644\nBinary files a/logo.png and /dev/null differ\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(dir, "prompt.txt")
	if err := run([]string{"prepare", "--task", taskPath, "--title", titlePath, "--diff", diffPath, "--out", promptPath}, io.Discard, io.Discard); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "gone.go") || !strings.Contains(string(prompt), "logo.png") {
		t.Fatalf("prepare dropped file names:\n%s", prompt)
	}

	inPath := filepath.Join(dir, "model.md")
	if err := os.WriteFile(inPath, []byte(validModelBody()), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.md")
	if err := run([]string{"sanitize", "--template", tmplPath, "--in", inPath, "--out", outPath}, io.Discard, io.Discard); err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "[x]") {
		t.Fatalf("sanitize left a checked box:\n%s", out)
	}

	badPath := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(badPath, []byte("## Summary\n\nonly one section\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"sanitize", "--template", tmplPath, "--in", badPath, "--out", outPath}, io.Discard, io.Discard)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitIneligible {
		t.Fatalf("malformed model output: got %v, want exit %d", err, exitIneligible)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()
	err := run([]string{"explode"}, io.Discard, io.Discard)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitUsage {
		t.Fatalf("got %v, want usage exit", err)
	}
}

func TestEligibleBareProse(t *testing.T) {
	t.Parallel()
	if Eligible("please merge this", repoTemplate(t)) {
		t.Fatal("prose with no template headings must not be overwritten")
	}
}

func TestSanitizeRejectsTemplateWithoutValidation(t *testing.T) {
	t.Parallel()
	_, err := Sanitize(validModelBody(), "## Summary\n")
	if err == nil {
		t.Fatal("expected an error when the template has no Validation section")
	}
}
