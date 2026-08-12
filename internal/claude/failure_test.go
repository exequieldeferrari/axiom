package claude_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// failurePayload is a PostToolUseFailure hook payload carrying one exact error
// string. The string is marshalled rather than written into the JSON by hand,
// so a test can hold newlines and whitespace the way Claude Code sends them.
func failurePayload(tool, errText string, interrupt bool) string {
	p := map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"session_id":      "abc",
		"tool_name":       tool,
		"error":           errText,
	}
	if interrupt {
		p["is_interrupt"] = true
	}
	out, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func reportOf(t *testing.T, tool, errText string) *event.Failure {
	t.Helper()

	f := ingest(t, failurePayload(tool, errText, false)).Tool.Failure
	if f == nil {
		t.Fatal("failure = nil, want details")
	}
	return f
}

// How the adapter classifies each shape of failure report, and which exit
// status it reads out of the same reading.
//
// The shapes marked as captured are the exact representations controlled runs
// of Claude Code produced. The rest are shapes it was never observed
// producing, and are here to pin what happens when it does: an unfamiliar
// report is never taken for one that said nothing.
func TestFailureReportClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tool     string
		errText  string
		want     event.Reporting
		wantCode *int
	}{
		{
			name:     "captured: a command that exited 1 having printed nothing",
			tool:     "Bash",
			errText:  "Exit code 1",
			want:     event.ReportingStatusOnly,
			wantCode: ptr(1),
		},
		{
			name:     "captured: another status reported alone",
			tool:     "Bash",
			errText:  "Exit code 2",
			want:     event.ReportingStatusOnly,
			wantCode: ptr(2),
		},
		{
			name:     "captured: a status followed by what the command printed",
			tool:     "Bash",
			errText:  "Exit code 1\nls: /nope: No such file or directory",
			want:     event.ReportingDetail,
			wantCode: ptr(1),
		},
		{
			name:     "captured: a failing test reported over several lines",
			tool:     "Bash",
			errText:  "Exit code 1\n--- FAIL: TestStock (0.00s)\n    stock_test.go:12: got 3, want 4\nFAIL",
			want:     event.ReportingDetail,
			wantCode: ptr(1),
		},
		{
			name:     "captured: a tool that is not the shell",
			tool:     "Read",
			errText:  "File does not exist.",
			want:     event.ReportingUnrecognized,
			wantCode: nil,
		},
		{
			name:    "a status line the agent indented",
			tool:    "Bash",
			errText: "  Exit code 1",
			// The exit status was already read through a trimmed line, and
			// one reading serves both, so neither can drift from the other.
			want:     event.ReportingStatusOnly,
			wantCode: ptr(1),
		},
		{
			name:    "a status the agent followed with a blank line",
			tool:    "Bash",
			errText: "Exit code 1\n   \n",
			// Never captured. Something came after the status and it is not
			// content, which is neither shape the adapter knows, so it says
			// so rather than deciding which one it resembles.
			want:     event.ReportingUnrecognized,
			wantCode: ptr(1),
		},
		{
			name:     "a status the agent terminated with a newline",
			tool:     "Bash",
			errText:  "Exit code 1\n",
			want:     event.ReportingUnrecognized,
			wantCode: ptr(1),
		},
		{
			name:    "output that looks like a status of its own",
			tool:    "Bash",
			errText: "Exit code 1\nmake: *** [test] Exit code 2",
			// The status is read from the first line only, so text further
			// down cannot be mistaken for the one the call exited with.
			want:     event.ReportingDetail,
			wantCode: ptr(1),
		},
		{
			name:    "a status that arrives after the output",
			tool:    "Bash",
			errText: "build failed\nExit code 1",
			// Never captured, and the adapter will not go looking: a report
			// whose first line is not a status is a shape it cannot read.
			want:     event.ReportingUnrecognized,
			wantCode: nil,
		},
		{
			name:     "a status that is not a number",
			tool:     "Bash",
			errText:  "Exit code oops",
			want:     event.ReportingUnrecognized,
			wantCode: nil,
		},
		{
			name:     "a report in no shape the adapter knows",
			tool:     "Bash",
			errText:  "could not start shell",
			want:     event.ReportingUnrecognized,
			wantCode: nil,
		},
		{
			name:     "a report of whitespace alone",
			tool:     "Bash",
			errText:  "   \n  ",
			want:     event.ReportingUnrecognized,
			wantCode: nil,
		},
		{
			name:     "no report at all",
			tool:     "Bash",
			errText:  "",
			want:     event.ReportingNoText,
			wantCode: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := reportOf(t, tt.tool, tt.errText)
			if f.Reporting != tt.want {
				t.Errorf("reporting = %q, want %q", f.Reporting, tt.want)
			}
			switch {
			case tt.wantCode == nil && f.ExitCode != nil:
				t.Errorf("exit_code = %d, want none", *f.ExitCode)
			case tt.wantCode != nil && f.ExitCode == nil:
				t.Errorf("exit_code = none, want %d", *tt.wantCode)
			case tt.wantCode != nil && *f.ExitCode != *tt.wantCode:
				t.Errorf("exit_code = %d, want %d", *f.ExitCode, *tt.wantCode)
			}
		})
	}
}

// A call someone stopped is classified on the text it came with, like any
// other. What the interruption means is carried by the kind, not by this.
func TestAnInterruptedCallIsClassifiedOnItsReport(t *testing.T) {
	t.Parallel()

	ev := ingest(t, failurePayload("Bash", "aborted", true))

	f := ev.Tool.Failure
	if f.Kind != event.FailureKindInterrupt {
		t.Errorf("kind = %q, want an interrupt", f.Kind)
	}
	if f.Reporting != event.ReportingUnrecognized {
		t.Errorf("reporting = %q, want it unrecognized", f.Reporting)
	}
}

// A report with no text has nothing to digest, and one Axiom could not read
// still has one: the digest says whether two reports were the same string,
// which does not require understanding either of them.
func TestAReportWithNoTextIsNotDigested(t *testing.T) {
	t.Parallel()

	if got := reportOf(t, "Bash", "").Digest; got != "" {
		t.Errorf("digest = %q, want none when nothing was reported", got)
	}
	if got := reportOf(t, "Bash", "could not start shell").Digest; got == "" {
		t.Error("digest is empty, want one for a report that was made")
	}
}

// The classification stands in for the text and must give away less than it
// did. Everything the adapter keeps has to survive the report being secret.
func TestClassificationCarriesNothingOfTheReport(t *testing.T) {
	t.Parallel()

	const secret = "Exit code 1\nfatal: Authentication failed for 'https://user:hunter2@example.com/'"

	stored, err := json.Marshal(reportOf(t, "Bash", secret))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(stored), "hunter2") {
		t.Errorf("the reported text reached the record: %s", stored)
	}
	// Every recognized value is a fixed word chosen from a closed set, so a
	// record can hold no more of the report than which set member it was.
	var f event.Failure
	if err := json.Unmarshal(stored, &f); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	switch f.Reporting {
	case event.ReportingDetail, event.ReportingStatusOnly,
		event.ReportingUnrecognized, event.ReportingNoText:
	default:
		t.Errorf("reporting = %q, want one of the classifications", f.Reporting)
	}
}

// A record written before reports were classified stays exactly as unhelpful
// as it was. Reading its silence as any classification would answer from
// evidence that was never recorded.
func TestAnUnclassifiedRecordStaysUnclassified(t *testing.T) {
	t.Parallel()

	var f event.Failure
	if err := json.Unmarshal([]byte(`{"kind":"error","digest":"abc","exit_code":1}`), &f); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if f.Reporting != "" {
		t.Errorf("reporting = %q, want it absent", f.Reporting)
	}
	// And a record that carries no classification writes none back, so a log
	// rewritten by a newer Axiom does not gain evidence it never had.
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "reporting") {
		t.Errorf("an absent classification was written out: %s", out)
	}
}
