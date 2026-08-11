package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/otlp"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// realExport is a captured Claude Code export, sanitized. Using the real
// payload here means the command is exercised against the wire shape it will
// actually meet.
func realExport(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile("../otlp/testdata/claude_logs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// receiver runs observe on an unused port and returns a function that stops it
// and yields everything it printed.
func receiver(t *testing.T, dir string) (endpoint string, stop func() string) {
	t.Helper()

	s, err := store.OpenUsage(dir)
	if err != nil {
		t.Fatalf("OpenUsage: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- observe(ctx, listener, s, &out, time.Now) }()

	return "http://" + listener.Addr().String() + otlp.LogsPath, func() string {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("observe: %v", err)
		}
		return out.String()
	}
}

func send(t *testing.T, endpoint string, body []byte) *http.Response {
	t.Helper()

	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestObserveRecordsUsage(t *testing.T) {
	dir := t.TempDir()
	endpoint, stop := receiver(t, dir)

	if resp := send(t, endpoint, realExport(t)); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	out := stop()

	scanner, err := store.ScanUsage(dir)
	if err != nil {
		t.Fatalf("ScanUsage: %v", err)
	}
	defer scanner.Close()

	var kinds []event.UsageKind
	for scanner.Scan() {
		kinds = append(kinds, scanner.Record().Kind)
	}
	// The export carries four records; only the two measurements are kept.
	if len(kinds) != 2 {
		t.Fatalf("recorded %d records, want 2: %v", len(kinds), kinds)
	}
	if kinds[0] != event.UsageModelRequest || kinds[1] != event.UsageToolResult {
		t.Errorf("kinds = %v", kinds)
	}
	if !strings.Contains(out, "Recorded 2 usage records") {
		t.Errorf("summary missing from output:\n%s", out)
	}
}

// The two streams are independent in this milestone. Reading the event log to
// enrich this output would quietly create a dependency between them.
func TestObserveNeverTouchesTheEventLog(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, store.EventsFile)
	if err := os.WriteFile(events, []byte("sentinel\n"), 0o600); err != nil {
		t.Fatalf("seed event log: %v", err)
	}
	before, err := os.Stat(events)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	endpoint, stop := receiver(t, dir)
	send(t, endpoint, realExport(t))
	stop()

	after, err := os.Stat(events)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("the event log was modified")
	}
	content, err := os.ReadFile(events)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "sentinel\n" {
		t.Errorf("the event log was rewritten: %q", content)
	}
}

// A record is described by its own fields. A tool name that telemetry did not
// report is not looked up anywhere.
func TestObservePrintsOnlyWhatTelemetryReported(t *testing.T) {
	dir := t.TempDir()
	endpoint, stop := receiver(t, dir)
	send(t, endpoint, realExport(t))
	out := stop()

	for _, want := range []string{
		"tool_result",
		"Read",
		"43 B returned",
		"model_request",
		"claude-sonnet-5",
		"2 in · 93 out · 0 cache read · 35419 cache write",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output omits %q:\n%s", want, out)
		}
	}
	// Nothing the allowlist drops may be printed either.
	for _, leaked := range []string{"developer@example.com", "user_0000", "xterm-256color"} {
		if strings.Contains(out, leaked) {
			t.Errorf("output contains %q:\n%s", leaked, out)
		}
	}
}

func TestObserveAnnouncesWhereItListens(t *testing.T) {
	dir := t.TempDir()
	endpoint, stop := receiver(t, dir)
	out := stop()

	if !strings.Contains(out, endpoint) {
		t.Errorf("output omits the endpoint %q:\n%s", endpoint, out)
	}
	if !strings.Contains(out, store.UsageFile) {
		t.Errorf("output omits where records are written:\n%s", out)
	}
	if !strings.Contains(out, "Recorded 0 usage records") {
		t.Errorf("an idle session reported no summary:\n%s", out)
	}
}

// An export Axiom cannot read is reported, because silence about it looks the
// same as an idle agent.
func TestObserveReportsRejectedExports(t *testing.T) {
	dir := t.TempDir()
	endpoint, stop := receiver(t, dir)

	if resp := send(t, endpoint, []byte("{not json")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	out := stop()

	if !strings.Contains(out, "1 export rejected as unreadable") {
		t.Errorf("output omits the rejection:\n%s", out)
	}
}

// Nothing is written until an export arrives, so a receiver left running does
// not litter the data directory.
func TestObserveWritesNothingUntilTelemetryArrives(t *testing.T) {
	dir := t.TempDir()
	_, stop := receiver(t, dir)
	stop()

	if _, err := os.Stat(filepath.Join(dir, store.UsageFile)); !os.IsNotExist(err) {
		t.Errorf("the usage log exists after an idle session: %v", err)
	}
}

func TestObserveRejectsUnknownFlags(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--nope"}, {"extra"}} {
		if _, err := parseObserveFlags(args); !IsUsage(err) {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
}

func TestObserveDefaultsToTheStandardPort(t *testing.T) {
	t.Parallel()

	addr, err := parseObserveFlags(nil)
	if err != nil {
		t.Fatalf("parseObserveFlags: %v", err)
	}
	if addr != DefaultAddr {
		t.Errorf("addr = %q, want %q", addr, DefaultAddr)
	}
	// Loopback only: telemetry describes what a developer is working on.
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("the default address is not loopback: %q", addr)
	}
	if custom, err := parseObserveFlags([]string{"--addr", "127.0.0.1:4319"}); err != nil || custom != "127.0.0.1:4319" {
		t.Errorf("--addr = %q, %v", custom, err)
	}
}

func TestDescribeUsageWithoutMeasurements(t *testing.T) {
	t.Parallel()

	// Missing telemetry is unknown, and the output has to say so rather than
	// print a zero.
	line := describeUsage(event.Usage{Kind: event.UsageModelRequest, Model: "claude-sonnet-5"})
	if !strings.Contains(line, "tokens not reported") {
		t.Errorf("line = %q", line)
	}
	line = describeUsage(event.Usage{Kind: event.UsageToolResult, ToolName: "Bash"})
	if !strings.Contains(line, "unreported number of bytes") {
		t.Errorf("line = %q", line)
	}

	big := int64(2048)
	line = describeUsage(event.Usage{Kind: event.UsageToolResult, ToolName: "Read", ResultBytes: &big})
	if !strings.Contains(line, "2.0 KB") {
		t.Errorf("line = %q", line)
	}
}
