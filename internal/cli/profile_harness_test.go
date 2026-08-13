package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// harnessProject builds a project the hook path can observe, with the files
// named by rel written into it.
func harnessProject(t *testing.T, files map[string]string) string {
	t.Helper()

	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// recordStart runs the hook path over one session start in root.
func recordStart(t *testing.T, dataDir, session, root string) {
	t.Helper()

	payload := `{"hook_event_name":"SessionStart","session_id":"` + session +
		`","cwd":"` + root + `","source":"startup"}`
	if err := runClaudeHook(strings.NewReader(payload), dataDir, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}
}

// recordedStarts decodes the session starts a hook run wrote.
func recordedStarts(t *testing.T, dataDir string) []event.Event {
	t.Helper()

	var out []event.Event
	for _, line := range logLines(t, dataDir) {
		var ev event.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stored line is not valid JSON: %v", err)
		}
		if ev.Type == event.TypeSessionStart {
			out = append(out, ev)
		}
	}
	return out
}

func digestAt(t *testing.T, ev event.Event, path string) string {
	t.Helper()

	if ev.Session == nil || ev.Session.Harness == nil {
		t.Fatal("the recorded start carries no harness provenance")
	}
	for _, c := range ev.Session.Harness.Components {
		if c.Path == path {
			return c.Digest
		}
	}
	t.Fatalf("%s was not observed", path)
	return ""
}

// Provenance is recorded while the session is starting, by the hook that
// observes it. Nothing else in Axiom is in a position to establish it.
func TestHookRecordsProvenanceAtSessionStart(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	dir := t.TempDir()

	recordStart(t, dir, "s1", root)

	starts := recordedStarts(t, dir)
	if len(starts) != 1 {
		t.Fatalf("recorded %d session starts, want 1", len(starts))
	}
	if digestAt(t, starts[0], "CLAUDE.md") == "" {
		t.Error("the observed instruction file recorded no digest")
	}
}

// The whole point of observing at capture time. A file that changes after a
// session was recorded cannot change what that session was recorded under.
func TestChangingAFileDoesNotAlterWhatWasRecorded(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# monday\n"})
	dir := t.TempDir()

	recordStart(t, dir, "monday", root)
	monday := digestAt(t, recordedStarts(t, dir)[0], "CLAUDE.md")

	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# tuesday\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	recordStart(t, dir, "tuesday", root)

	starts := recordedStarts(t, dir)
	if len(starts) != 2 {
		t.Fatalf("recorded %d session starts, want 2", len(starts))
	}
	if again := digestAt(t, starts[0], "CLAUDE.md"); again != monday {
		t.Errorf("the earlier record now reads %s, want the %s it was written with", again, monday)
	}
	if tuesday := digestAt(t, starts[1], "CLAUDE.md"); tuesday == monday {
		t.Error("a changed file was recorded with the earlier digest")
	}

	// Both are still in the report, under the sessions that observed them.
	out := scopedProfileOutput(t, dir, profileOptions{})
	if !strings.Contains(out, "session monday") || !strings.Contains(out, "session tuesday") {
		t.Errorf("both sessions should appear:\n%s", out)
	}
}

// A record written before Axiom observed any of this says no provenance was
// recorded. It must not be filled in from the machine reading the log, which
// would describe today and attribute it to work recorded earlier.
func TestHistoricalRecordsAreNotReconstructed(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# present day\n"})
	dir := t.TempDir()

	s, err := store.OpenEvents(dir)
	if err != nil {
		t.Fatalf("OpenEvents: %v", err)
	}
	// A start exactly as an older Axiom wrote it: a real working directory,
	// with configuration sitting in it now, and no provenance on the record.
	historical := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionStart,
		Timestamp:     hookNow,
		SessionID:     "historical",
		Cwd:           root,
		Session:       &event.Session{Source: "startup"},
	}
	if err := s.Append(historical); err != nil {
		t.Fatalf("Append: %v", err)
	}

	out := scopedProfileOutput(t, dir, profileOptions{})
	if !strings.Contains(out, "No harness provenance was recorded.") {
		t.Errorf("want the empty state, got:\n%s", out)
	}
	if strings.Contains(out, "observed  ") {
		t.Errorf("provenance was reconstructed from the filesystem:\n%s", out)
	}
	for _, forbidden := range []string{"default harness", "no configuration", "unconfigured"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("the empty state claims %q:\n%s", forbidden, out)
		}
	}
}

// Two sessions recorded under different configuration keep their own
// observations, and the report shows both rather than one harness for the log.
func TestTwoSessionsWithDifferentProvenance(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# before\n"})
	dir := t.TempDir()

	recordStart(t, dir, "before", root)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# after\n"), 0o600); err != nil {
		t.Fatalf("rewrite instructions: %v", err)
	}
	recordStart(t, dir, "after", root)

	starts := recordedStarts(t, dir)
	first, second := digestAt(t, starts[0], "CLAUDE.md"), digestAt(t, starts[1], "CLAUDE.md")
	if first == second {
		t.Fatal("the two sessions recorded the same digest")
	}

	out := scopedProfileOutput(t, dir, profileOptions{})
	for _, want := range []string{shortDigest(first), shortDigest(second)} {
		if !strings.Contains(out, want) {
			t.Errorf("the report omits %s:\n%s", want, out)
		}
	}
}

// The log is a metadata log. Configuration contents must not reach it through
// this path, whatever the configuration says.
func TestNoConfigurationContentReachesTheLog(t *testing.T) {
	const secret = "sk-ant-not-a-real-key"
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":                   "always use " + secret + "\n",
		".claude/settings.local.json": `{"env":{"TOKEN":"` + secret + `"}}`,
	})
	dir := t.TempDir()

	recordStart(t, dir, "s1", root)

	written, err := os.ReadFile(filepath.Join(dir, store.EventsFile))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, fragment := range []string{secret, "always use", "TOKEN"} {
		if strings.Contains(string(written), fragment) {
			t.Errorf("the log carries %q:\n%s", fragment, written)
		}
	}

	// Nor may it reach the report, which is the other place it could
	// surface.
	if out := scopedProfileOutput(t, dir, profileOptions{}); strings.Contains(out, secret) {
		t.Errorf("the report carries the secret:\n%s", out)
	}
}

// A project Axiom cannot observe costs provenance and never the session. Axiom
// is a passive observer, and a start that went unrecorded would be a session
// missing from every report below.
func TestAnUnobservableProjectStillRecordsTheStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	payload := `{"hook_event_name":"SessionStart","session_id":"s1","source":"startup"}`
	if err := runClaudeHook(strings.NewReader(payload), dir, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}

	starts := recordedStarts(t, dir)
	if len(starts) != 1 {
		t.Fatalf("recorded %d session starts, want 1", len(starts))
	}
	if starts[0].Session.Harness != nil {
		t.Errorf("recorded provenance for a session with no project: %+v", starts[0].Session.Harness)
	}
	if out := scopedProfileOutput(t, dir, profileOptions{}); !strings.Contains(out, "No harness provenance was recorded.") {
		t.Errorf("want the empty state, got:\n%s", out)
	}
}

// Where one session recorded provenance and another did not, the second is
// accounted for rather than dropped: its silence is the reason the report does
// not cover the log.
func TestAStartWithoutProvenanceIsAccountedFor(t *testing.T) {
	root := harnessProject(t, map[string]string{"CLAUDE.md": "# guidance\n"})
	dir := t.TempDir()

	recordStart(t, dir, "observed", root)
	blind := `{"hook_event_name":"SessionStart","session_id":"unobserved","source":"startup"}`
	if err := runClaudeHook(strings.NewReader(blind), dir, hookNow); err != nil {
		t.Fatalf("runClaudeHook: %v", err)
	}

	out := scopedProfileOutput(t, dir, profileOptions{})
	if !strings.Contains(out, "session unobserved") {
		t.Errorf("the session that recorded nothing is missing:\n%s", out)
	}
	if !strings.Contains(out, "no harness provenance recorded at 1 recorded session start") {
		t.Errorf("the start is not accounted for:\n%s", out)
	}
}

// The whole path, from a hostile repository to the page a user reads. A clone
// can carry a link naming a private file, and the guarantee is that neither
// the file nor its name gets anywhere: not into the log, not into the report,
// and not into the digest either.
func TestALinkOutOfTheProjectIsRefusedEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on windows")
	}

	const secret = "aws_secret_access_key = ...\n"
	root := harnessProject(t, nil)
	outside := t.TempDir()
	target := filepath.Join(outside, "private-credentials.md")
	if err := os.WriteFile(target, []byte(secret), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	dir := t.TempDir()

	recordStart(t, dir, "s1", root)

	// The start is recorded. A refusal is an observation with a result, not
	// a failure that costs the event.
	if d := digestAt(t, recordedStarts(t, dir)[0], "CLAUDE.md"); d != "" {
		t.Errorf("a link out of the project recorded a digest %q", d)
	}

	written, err := os.ReadFile(filepath.Join(dir, store.EventsFile))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	out := scopedProfileOutput(t, dir, profileOptions{})
	// The target's name, and the digest of its bytes, which is what a read
	// of it would have left behind.
	for _, leaked := range []string{target, outside, "private-credentials", digest.HarnessFile([]byte(secret))} {
		if strings.Contains(string(written), leaked) {
			t.Errorf("the log carries %q:\n%s", leaked, written)
		}
		if strings.Contains(out, leaked) {
			t.Errorf("the report carries %q:\n%s", leaked, out)
		}
	}
	// What the report names is the eligible path, and what it says about it
	// is that Axiom stopped there.
	if !strings.Contains(out, "CLAUDE.md                       link not followed") {
		t.Errorf("the report does not show the link as unfollowed:\n%s", out)
	}
}

// Each state has to reach the page as itself. A component Axiom could not
// establish must not read as one that was not there.
func TestEveryComponentStateIsRendered(t *testing.T) {
	dir := seed(t, event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         "claude-code",
		Type:          event.TypeSessionStart,
		Timestamp:     hookNow,
		SessionID:     "s1",
		Session: &event.Session{Source: "startup", Harness: &event.Harness{
			Components: []event.HarnessComponent{
				{Kind: event.HarnessProjectInstructions, Path: "CLAUDE.md",
					Status: event.HarnessUnreadable},
				{Kind: event.HarnessProjectSettings, Path: ".claude/settings.json",
					Status: event.HarnessAbsent},
				{Kind: event.HarnessLocalProjectSettings, Path: ".claude/settings.local.json",
					Status: event.HarnessNotFollowed},
				{Kind: event.HarnessSubagentDirectory, Path: ".claude/agents",
					Status: event.HarnessObserved},
			},
		}},
	})

	out := scopedProfileOutput(t, dir, profileOptions{})
	for _, want := range []string{
		"CLAUDE.md                       not established",
		".claude/settings.json           nothing found there",
		".claude/settings.local.json     link not followed",
		".claude/agents                  enumerated, no definition found",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report omits %q:\n%s", want, out)
		}
	}
}

// A session that started many times keeps its most recent observations, and
// accounts for the ones it left out rather than dropping them silently.
func TestOlderObservationsAreAccountedFor(t *testing.T) {
	var events []event.Event
	for i := range 7 {
		events = append(events, event.Event{
			SchemaVersion: event.SchemaVersion,
			Agent:         "claude-code",
			Type:          event.TypeSessionStart,
			Timestamp:     hookNow,
			SessionID:     "s1",
			Session: &event.Session{Source: "resume", Harness: &event.Harness{
				Components: []event.HarnessComponent{{
					Kind:   event.HarnessProjectInstructions,
					Path:   "CLAUDE.md",
					Status: event.HarnessObserved,
					Digest: strings.Repeat(string(rune('a'+i)), 64),
				}},
			}},
		})
	}

	out := scopedProfileOutput(t, seed(t, events...), profileOptions{})
	if !strings.Contains(out, "3 earlier observations omitted") {
		t.Errorf("the omitted observations are not accounted for:\n%s", out)
	}
	if !strings.Contains(out, "session start 7") {
		t.Errorf("the most recent observation is missing:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("a", 12)) {
		t.Errorf("an omitted observation was printed anyway:\n%s", out)
	}
}

// A file the agent would not read is not part of the project's provenance,
// however plausible its name.
func TestAnIneligibleFileIsNotObserved(t *testing.T) {
	root := harnessProject(t, map[string]string{
		"CLAUDE.md":            "# the project\n",
		"docs/CLAUDE.md":       "# not the project's\n",
		"CLAUDE.local.md":      "# not eligible\n",
		".claude/agents/x.txt": "not a definition",
	})
	dir := t.TempDir()

	recordStart(t, dir, "s1", root)

	observed := recordedStarts(t, dir)[0].Session.Harness.Components
	for _, c := range observed {
		switch c.Path {
		case "docs/CLAUDE.md", "CLAUDE.local.md", ".claude/agents/x.txt":
			t.Errorf("%s was observed, and the agent's reading of it is not established", c.Path)
		}
	}
}
