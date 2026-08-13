package claude_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
)

// project builds a directory Claude Code would treat as one, with the files
// named by rel written into it.
func project(t *testing.T, files map[string]string) string {
	t.Helper()

	// A home directory of its own keeps root resolution away from whatever
	// the machine running the tests has above the temporary directory.
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	for rel, content := range files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// observe runs the collector the way the hook path does.
func observe(t *testing.T, cwd string) *event.Harness {
	t.Helper()

	return observeIn(cwd)
}

// observeIn is observe without the test, for the one caller that runs the
// collector on a goroutine of its own and cannot report from there.
func observeIn(cwd string) *event.Harness {
	ev := &event.Event{
		Type:    event.TypeSessionStart,
		Cwd:     cwd,
		Session: &event.Session{Source: "startup"},
	}
	claude.ObserveHarness(ev)
	return ev.Session.Harness
}

// components maps a harness by path so a test can name the one it means.
func components(t *testing.T, h *event.Harness) map[string]event.HarnessComponent {
	t.Helper()

	if h == nil {
		t.Fatal("no harness provenance was recorded")
	}
	out := make(map[string]event.HarnessComponent, len(h.Components))
	for _, c := range h.Components {
		out[c.Path] = c
	}
	return out
}

// componentStatus is what was established about one path.
func componentStatus(t *testing.T, h *event.Harness, path string) event.HarnessStatus {
	t.Helper()

	return components(t, h)[path].Status
}

func TestObserveRecordsTheEligiblePaths(t *testing.T) {
	root := project(t, map[string]string{
		"CLAUDE.md":                   "# instructions\n",
		".claude/settings.local.json": `{"hooks":{}}`,
		".claude/agents/reviewer.md":  "review things",
	})

	got := components(t, observe(t, root))

	want := map[string]event.HarnessStatus{
		"CLAUDE.md":                   event.HarnessObserved,
		".claude/settings.json":       event.HarnessAbsent,
		".claude/settings.local.json": event.HarnessObserved,
		".claude/agents":              event.HarnessObserved,
		".claude/agents/reviewer.md":  event.HarnessObserved,
	}
	if len(got) != len(want) {
		t.Fatalf("observed %d components, want %d: %+v", len(got), len(want), got)
	}
	for path, status := range want {
		c, ok := got[path]
		if !ok {
			t.Fatalf("%s was not observed", path)
		}
		if c.Status != status {
			t.Errorf("%s status = %q, want %q", path, c.Status, status)
		}
	}

	// A digest belongs to a file that was read, and to nothing else. A
	// directory carries none: it has no bytes, and one there would invite
	// being read as an identity for everything under it.
	if got["CLAUDE.md"].Digest == "" {
		t.Error("an observed file recorded no digest")
	}
	if d := got[".claude/agents"].Digest; d != "" {
		t.Errorf("the definitions directory recorded a digest %q", d)
	}
}

// The order components are recorded in cannot depend on the filesystem, or two
// observations of one unchanged project would differ.
func TestObserveIsDeterministicallyOrdered(t *testing.T) {
	files := map[string]string{"CLAUDE.md": "a"}
	for _, name := range []string{"zulu", "alpha", "mike", "bravo"} {
		files[".claude/agents/"+name+".md"] = name
	}
	root := project(t, files)

	var paths []string
	for _, c := range observe(t, root).Components {
		paths = append(paths, c.Path)
	}
	want := []string{
		"CLAUDE.md",
		".claude/settings.json",
		".claude/settings.local.json",
		".claude/agents",
		".claude/agents/alpha.md",
		".claude/agents/bravo.md",
		".claude/agents/mike.md",
		".claude/agents/zulu.md",
	}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", paths, want)
	}

	// Observing the same unchanged project twice has to produce the same
	// record, or nothing recorded could ever be compared to anything.
	first, _ := json.Marshal(observe(t, root))
	second, _ := json.Marshal(observe(t, root))
	if string(first) != string(second) {
		t.Errorf("two observations of one project differ:\n%s\n%s", first, second)
	}
}

func TestDigestIdentifiesExactBytes(t *testing.T) {
	same := digestOf(t, "# guidance\n")
	if again := digestOf(t, "# guidance\n"); again != same {
		t.Errorf("the same bytes digested differently: %s and %s", same, again)
	}
	// One byte is the whole promise: a change the digest cannot see is a
	// change the record would silently call no change at all.
	if changed := digestOf(t, "# guidancE\n"); changed == same {
		t.Error("a one byte change produced the same digest")
	}
	// Nothing is normalized on the way in, so a file that differs only in
	// trailing whitespace is a different file.
	if trailing := digestOf(t, "# guidance"); trailing == same {
		t.Error("a missing trailing newline produced the same digest")
	}
}

func digestOf(t *testing.T, content string) string {
	t.Helper()

	root := project(t, map[string]string{"CLAUDE.md": content})
	return components(t, observe(t, root))["CLAUDE.md"].Digest
}

// Nothing Axiom reads may survive in what it writes. A digest is the point of
// the design, and a record that carried a line of the file would defeat it.
func TestNoContentIsRecorded(t *testing.T) {
	const secret = "ANTHROPIC_API_KEY=sk-ant-not-a-real-key"
	root := project(t, map[string]string{
		"CLAUDE.md":                   "never write " + secret + " anywhere\n",
		".claude/settings.local.json": `{"env":{"SECRET":"` + secret + `"}}`,
		".claude/agents/leak.md":      secret,
	})

	recorded, err := json.Marshal(observe(t, root))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, fragment := range []string{secret, "sk-ant", "ANTHROPIC_API_KEY", "never write", "env"} {
		if strings.Contains(string(recorded), fragment) {
			t.Errorf("the record carries %q:\n%s", fragment, recorded)
		}
	}
}

// A component Axiom could not read is not a component that was not there. The
// two states drive different readings and are never allowed to collapse.
func TestUnreadableIsNotAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}

	root := project(t, map[string]string{"CLAUDE.md": "x"})
	if err := os.Chmod(filepath.Join(root, "CLAUDE.md"), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "CLAUDE.md"), 0o600) })

	c := components(t, observe(t, root))["CLAUDE.md"]
	if c.Status != event.HarnessUnreadable {
		t.Errorf("status = %q, want %q", c.Status, event.HarnessUnreadable)
	}
	if c.Digest != "" {
		t.Errorf("an unreadable file recorded a digest %q", c.Digest)
	}
}

// A directory where a file is expected is something Axiom cannot identify, not
// an absence.
func TestDirectoryInPlaceOfAFileIsUnreadable(t *testing.T) {
	root := project(t, nil)
	if err := os.Mkdir(filepath.Join(root, "CLAUDE.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if s := components(t, observe(t, root))["CLAUDE.md"].Status; s != event.HarnessUnreadable {
		t.Errorf("status = %q, want %q", s, event.HarnessUnreadable)
	}
}

// A file past the size Axiom will read is one it declined to identify. The
// bound exists because this runs while a user waits for a session to start,
// and because a record too large for the store would cost the session start.
func TestOversizedFileIsUnreadable(t *testing.T) {
	root := project(t, map[string]string{
		"CLAUDE.md": strings.Repeat("x", claude.MaxComponentBytes+1),
	})

	if s := components(t, observe(t, root))["CLAUDE.md"].Status; s != event.HarnessUnreadable {
		t.Errorf("status = %q, want %q", s, event.HarnessUnreadable)
	}
}

// The agent reads through a symlink, so Axiom follows one — but only where it
// leads to a file in the same project. A project that keeps its instructions
// in docs and links them into place has instructions, and reporting nothing
// here would describe a project nobody has.
func TestSymlinkInsideTheProjectIsFollowed(t *testing.T) {
	requireSymlinks(t)

	root := project(t, map[string]string{"docs/instructions.md": "# shared\n"})
	link(t, "docs/instructions.md", filepath.Join(root, "CLAUDE.md"))

	observed := observe(t, root)
	c := components(t, observed)["CLAUDE.md"]
	if c.Status != event.HarnessObserved {
		t.Fatalf("status = %q, want %q", c.Status, event.HarnessObserved)
	}
	// The path recorded is the one Axiom looked at, not the one it read.
	if c.Path != "CLAUDE.md" {
		t.Errorf("path = %q, want the eligible path", c.Path)
	}
	if encoded, err := json.Marshal(observed); err != nil {
		t.Fatalf("marshal: %v", err)
	} else if strings.Contains(string(encoded), "docs/instructions.md") {
		t.Errorf("the record carries the link target:\n%s", encoded)
	}

	direct := project(t, map[string]string{"CLAUDE.md": "# shared\n"})
	if other := components(t, observe(t, direct))["CLAUDE.md"].Digest; other != c.Digest {
		t.Error("the same bytes through a link digested differently")
	}
}

// A repository is not trusted input. It may have been cloned from anywhere,
// and every link below is a filesystem entry a clone can carry, each one an
// attempt to have Axiom read a file the repository named and the user never
// offered. None of them is followed.
//
// That the bytes were not read is shown by the digest they would have
// produced appearing nowhere. The test after this one checks that the same
// bytes inside the project do produce that digest, so it is a value a read
// would leave behind and not one nothing ever matches.
func TestSymlinkOutOfTheProjectIsNotFollowed(t *testing.T) {
	requireSymlinks(t)

	const secret = "PRIVATE KEY MATERIAL\n"
	secretDigest := digest.HarnessFile([]byte(secret))

	for _, tc := range []struct {
		name string
		// build creates the eligible CLAUDE.md inside root, given a
		// directory outside the project holding secret.md.
		build func(t *testing.T, root, outside string)
	}{
		{"an absolute link out of the project", func(t *testing.T, root, outside string) {
			link(t, filepath.Join(outside, "secret.md"), filepath.Join(root, "CLAUDE.md"))
		}},
		{"a relative link climbing out with ..", func(t *testing.T, root, outside string) {
			rel, err := filepath.Rel(root, filepath.Join(outside, "secret.md"))
			if err != nil {
				t.Fatalf("relative target: %v", err)
			}
			if !strings.HasPrefix(rel, "..") {
				t.Fatalf("target %q does not climb out of the project", rel)
			}
			link(t, rel, filepath.Join(root, "CLAUDE.md"))
		}},
		{"a chain of links ending outside", func(t *testing.T, root, outside string) {
			link(t, filepath.Join(outside, "secret.md"), filepath.Join(root, "hop.md"))
			link(t, "hop.md", filepath.Join(root, "CLAUDE.md"))
		}},
		{"a link through a linked directory", func(t *testing.T, root, outside string) {
			link(t, outside, filepath.Join(root, "elsewhere"))
			link(t, "elsewhere/secret.md", filepath.Join(root, "CLAUDE.md"))
		}},
		{"an absolute link back into the project", func(t *testing.T, root, _ string) {
			// Refused too. Eligible paths are resolved inside an
			// open project root, which no absolute path is relative
			// to, and Axiom will not resolve one itself to find out
			// where it landed.
			write(t, filepath.Join(root, "docs", "secret.md"), secret)
			link(t, filepath.Join(root, "docs", "secret.md"), filepath.Join(root, "CLAUDE.md"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t, nil)
			outside := t.TempDir()
			write(t, filepath.Join(outside, "secret.md"), secret)
			tc.build(t, root, outside)

			observed := observe(t, root)
			c := components(t, observed)["CLAUDE.md"]
			if c.Status != event.HarnessNotFollowed {
				t.Errorf("status = %q, want %q", c.Status, event.HarnessNotFollowed)
			}
			if c.Digest != "" {
				t.Errorf("a link that was not followed recorded a digest %q", c.Digest)
			}

			encoded, err := json.Marshal(observed)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// The bytes were never hashed, so nothing in the record
			// can carry what hashing them produces.
			if strings.Contains(string(encoded), secretDigest) {
				t.Errorf("the record carries the digest of a file it must not read:\n%s", encoded)
			}
			// Nor is the target named. Where a link led may itself
			// be private, and it is not a fact about the project.
			for _, leaked := range []string{outside, "secret.md", "elsewhere"} {
				if strings.Contains(string(encoded), leaked) {
					t.Errorf("the record carries %q:\n%s", leaked, encoded)
				}
			}
		})
	}
}

// The control for the test above: the same bytes inside the project do produce
// that digest, so its absence there means the file was not read.
func TestTheRefusedDigestIsOneAReadWouldProduce(t *testing.T) {
	const secret = "PRIVATE KEY MATERIAL\n"
	root := project(t, map[string]string{"CLAUDE.md": secret})

	if d := components(t, observe(t, root))["CLAUDE.md"].Digest; d != digest.HarnessFile([]byte(secret)) {
		t.Errorf("digest = %q, want the digest of the bytes read", d)
	}
}

// A link leading nowhere leaves nothing to read, which is what absent says. It
// is not a refusal: Axiom followed this one, within the project, and the
// project has nothing there.
func TestDanglingSymlinkIsAbsent(t *testing.T) {
	requireSymlinks(t)

	root := project(t, nil)
	link(t, "gone.md", filepath.Join(root, "CLAUDE.md"))

	if s := components(t, observe(t, root))["CLAUDE.md"].Status; s != event.HarnessAbsent {
		t.Errorf("status = %q, want %q", s, event.HarnessAbsent)
	}
}

// The definitions directory is the entry where a link would matter most.
// Following one out of the project would not read a file the repository named:
// it would list a directory the repository chose and then read everything in
// it called .md.
func TestLinkedDefinitionsDirectoryIsNotEnumerated(t *testing.T) {
	requireSymlinks(t)

	root := project(t, nil)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "notes.md"), "private notes\n")
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link(t, outside, filepath.Join(root, ".claude", "agents"))

	got := components(t, observe(t, root))
	if s := got[".claude/agents"].Status; s != event.HarnessNotFollowed {
		t.Errorf("status = %q, want %q", s, event.HarnessNotFollowed)
	}
	// Nothing behind the link was enumerated, so nothing behind it was
	// named. A component for a file out there would be the escape itself.
	for path := range got {
		if strings.HasPrefix(path, ".claude/agents/") {
			t.Errorf("a definition outside the project was recorded: %s", path)
		}
	}
}

// The escape need not be the eligible path itself. Replacing the directory an
// eligible path hangs from points three components at once, and each is
// resolved through the project root a component at a time, so none of them
// arrives anywhere.
//
// These are reported as paths Axiom did not establish rather than as links,
// because none of them is a link: `.claude` is, and `.claude/settings.json` is
// a name that no longer resolves. Saying more would mean resolving it to find
// out where it went.
func TestALinkedParentDirectoryLeadsNowhere(t *testing.T) {
	requireSymlinks(t)

	const secret = "aws_secret_access_key = ...\n"
	root := project(t, nil)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "settings.json"), secret)
	link(t, outside, filepath.Join(root, ".claude"))

	observed := observe(t, root)
	got := components(t, observed)
	for _, path := range []string{".claude/settings.json", ".claude/settings.local.json", ".claude/agents"} {
		c := got[path]
		if c.Status != event.HarnessUnreadable {
			t.Errorf("%s status = %q, want %q", path, c.Status, event.HarnessUnreadable)
		}
		if c.Digest != "" {
			t.Errorf("%s recorded a digest %q", path, c.Digest)
		}
	}
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), digest.HarnessFile([]byte(secret))) {
		t.Errorf("the record carries the digest of a file it must not read:\n%s", encoded)
	}
}

// A ring of links resolves to nothing, and has to do so by being refused rather
// than by being walked. The observation finishes and records what it can.
func TestALoopOfLinksIsNotFollowed(t *testing.T) {
	requireSymlinks(t)

	root := project(t, nil)
	link(t, "loop.md", filepath.Join(root, "CLAUDE.md"))
	link(t, "CLAUDE.md", filepath.Join(root, "loop.md"))

	if s := componentStatus(t, observe(t, root), "CLAUDE.md"); s != event.HarnessNotFollowed {
		t.Errorf("status = %q, want %q", s, event.HarnessNotFollowed)
	}
}

// A definition among real ones is observed like any other file, and bounded
// like any other file.
func TestLinkedDefinitionOutOfTheProjectIsNotFollowed(t *testing.T) {
	requireSymlinks(t)

	const secret = "private notes\n"
	root := project(t, map[string]string{".claude/agents/real.md": "real"})
	outside := t.TempDir()
	write(t, filepath.Join(outside, "notes.md"), secret)
	link(t, filepath.Join(outside, "notes.md"), filepath.Join(root, ".claude", "agents", "linked.md"))

	observed := observe(t, root)
	got := components(t, observed)
	// The directory itself was read: it is inside the project, and what it
	// holds is a fact about the project even where a name in it leads out.
	if s := got[".claude/agents"].Status; s != event.HarnessObserved {
		t.Errorf("directory status = %q, want %q", s, event.HarnessObserved)
	}
	if s := got[".claude/agents/real.md"].Status; s != event.HarnessObserved {
		t.Errorf("real definition status = %q, want %q", s, event.HarnessObserved)
	}
	c := got[".claude/agents/linked.md"]
	if c.Status != event.HarnessNotFollowed {
		t.Errorf("linked definition status = %q, want %q", c.Status, event.HarnessNotFollowed)
	}
	if c.Digest != "" {
		t.Errorf("a link that was not followed recorded a digest %q", c.Digest)
	}
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), digest.HarnessFile([]byte(secret))) {
		t.Errorf("the record carries the digest of a file it must not read:\n%s", encoded)
	}
}

func requireSymlinks(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on windows")
	}
}

func link(t *testing.T, target, path string) {
	t.Helper()

	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %s: %v", path, err)
	}
}

// The definitions directory is always recorded, so that a project with none is
// distinguishable from an Axiom that did not enumerate any.
func TestMissingDefinitionsDirectoryIsRecorded(t *testing.T) {
	root := project(t, map[string]string{"CLAUDE.md": "x"})

	got := components(t, observe(t, root))
	c, ok := got[".claude/agents"]
	if !ok {
		t.Fatal("the definitions directory was not recorded at all")
	}
	if c.Status != event.HarnessAbsent {
		t.Errorf("status = %q, want %q", c.Status, event.HarnessAbsent)
	}
	for path := range got {
		if strings.HasPrefix(path, ".claude/agents/") {
			t.Errorf("a definition was recorded under a directory that is not there: %s", path)
		}
	}
}

// Enumeration is one level deep and covers definition files. Anything else in
// the directory is not part of the contract and is not recorded.
func TestEnumerationDoesNotRecurseOrWiden(t *testing.T) {
	root := project(t, map[string]string{
		".claude/agents/reviewer.md":      "review",
		".claude/agents/notes.txt":        "not a definition",
		".claude/agents/team/nested.md":   "nested",
		".claude/agents/team/deeper/x.md": "deeper",
	})

	var recorded []string
	for _, c := range observe(t, root).Components {
		if c.Kind == event.HarnessSubagentDefinition {
			recorded = append(recorded, c.Path)
		}
	}
	if len(recorded) != 1 || recorded[0] != ".claude/agents/reviewer.md" {
		t.Errorf("recorded %v, want only the top level definition", recorded)
	}
}

// Part of a set presented as the set would be the false claim this whole
// design exists to avoid, so a directory holding more than Axiom will read is
// reported as one it did not establish.
func TestTooManyDefinitionsLeavesTheDirectoryUnestablished(t *testing.T) {
	files := make(map[string]string, claude.MaxSubagentDefinitions+1)
	for i := 0; i <= claude.MaxSubagentDefinitions; i++ {
		files[fmt.Sprintf(".claude/agents/agent-%03d.md", i)] = "x"
	}
	root := project(t, files)

	got := observe(t, root)
	for _, c := range got.Components {
		if c.Kind == event.HarnessSubagentDefinition {
			t.Fatalf("a definition was recorded from a directory that was not established: %s", c.Path)
		}
		if c.Kind == event.HarnessSubagentDirectory && c.Status != event.HarnessUnreadable {
			t.Errorf("directory status = %q, want %q", c.Status, event.HarnessUnreadable)
		}
	}
}

// Configuration is looked for at the project root, which is where the agent
// reads it, and not in whichever subdirectory the agent happened to start in.
func TestObservationIsRootedAtTheProject(t *testing.T) {
	root := project(t, map[string]string{
		"CLAUDE.md":        "# the project\n",
		"vendor/CLAUDE.md": "# unrelated, and not the project's\n",
	})
	sub := filepath.Join(root, "vendor")

	fromRoot := components(t, observe(t, root))["CLAUDE.md"].Digest
	fromSub := components(t, observe(t, sub))["CLAUDE.md"].Digest
	if fromSub != fromRoot {
		t.Errorf("a session started in a subdirectory observed a different file: %s and %s", fromSub, fromRoot)
	}
}

// Without a working directory there is no project, and looking relative to the
// hook process would record a project that has nothing to do with the session.
func TestNoProjectRecordsNoProvenance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for name, cwd := range map[string]string{
		"no working directory":       "",
		"relative working directory": "project",
		"working directory gone":     filepath.Join(t.TempDir(), "removed"),
	} {
		t.Run(name, func(t *testing.T) {
			if h := observe(t, cwd); h != nil {
				t.Errorf("recorded %+v, want no provenance at all", h)
			}
		})
	}
}

// Provenance is observed for a session start and for nothing else. A tool call
// is not a point at which anything was established.
func TestOnlyASessionStartCarriesProvenance(t *testing.T) {
	root := project(t, map[string]string{"CLAUDE.md": "x"})

	for name, ev := range map[string]*event.Event{
		"tool call": {
			Type: event.TypeToolCall, Cwd: root,
			Tool: &event.ToolCall{Name: "Read", Outcome: event.OutcomeSuccess},
		},
		"session end": {
			Type: event.TypeSessionEnd, Cwd: root,
			Session: &event.Session{Reason: "other"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			claude.ObserveHarness(ev)
			if ev.Session != nil && ev.Session.Harness != nil {
				t.Errorf("recorded provenance on a %s", name)
			}
		})
	}

	// Nothing at all, on the path where the adapter produced no event.
	claude.ObserveHarness(nil)
}

// BenchmarkObserveHarness characterizes what this adds to the hook path.
//
// It runs once per session start and never on a tool call, so the cost is
// bounded by the eligible paths and the definitions beside them. The number to
// watch is the one with a full directory, because it is the worst a project
// can produce.
func BenchmarkObserveHarness(b *testing.B) {
	root := b.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		b.Fatalf("create .git: %v", err)
	}
	seed := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatalf("create directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	seed("CLAUDE.md", strings.Repeat("guidance\n", 500))
	seed(".claude/settings.local.json", `{"hooks":{}}`)
	for i := 0; i < claude.MaxSubagentDefinitions; i++ {
		seed(fmt.Sprintf(".claude/agents/agent-%03d.md", i), strings.Repeat("definition\n", 200))
	}

	ev := event.Event{Type: event.TypeSessionStart, Cwd: root, Session: &event.Session{}}
	for b.Loop() {
		claude.ObserveHarness(&ev)
	}
}

// The hook path must survive whatever a project looks like. A record the store
// would refuse to write costs the session start itself.
func TestObservationStaysWithinOneRecord(t *testing.T) {
	files := make(map[string]string, claude.MaxSubagentDefinitions)
	name := strings.Repeat("n", 250-len(".md"))
	for i := 0; i < claude.MaxSubagentDefinitions; i++ {
		files[fmt.Sprintf(".claude/agents/%s%03d.md", name[:len(name)-3], i)] = "x"
	}
	files["CLAUDE.md"] = "x"
	root := project(t, files)

	ev := event.Event{
		SchemaVersion: event.SchemaVersion,
		Agent:         claude.AgentName,
		Type:          event.TypeSessionStart,
		Timestamp:     time.Now().UTC(),
		SessionID:     "1b4b390f-9e79-483d-a293-c22b1b03bb5c",
		Cwd:           root,
		Session:       &event.Session{Source: "startup", Harness: observe(t, root)},
	}
	encoded, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) >= 64<<10 {
		t.Errorf("a full observation encodes to %d bytes, which the store would refuse", len(encoded))
	}
}
