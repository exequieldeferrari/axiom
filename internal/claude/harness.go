package claude

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/exequieldeferrari/axiom/internal/digest"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/project"
)

// Bounds on what one observation will read. Both exist because this runs on
// the hook path, in front of a user waiting for a session to start, and
// because a record the store refuses to write would cost the session start
// itself. Past either bound the component is recorded as unreadable, which is
// what it is: Axiom declined to establish its identity.
const (
	// MaxComponentBytes is the largest file that will be digested.
	MaxComponentBytes = 1 << 20
	// MaxSubagentDefinitions is the largest number of definitions that will
	// be enumerated in one directory.
	MaxSubagentDefinitions = 64
)

// Eligible paths, relative to the resolved project root.
//
// This list is the whole of what Axiom selects. It is fixed, it is short, and
// it holds only paths Claude Code reads for the project it was started in.
// Nothing is discovered: no directory is searched for candidates, the one
// directory that is enumerated is enumerated at a single level, and the user's
// home directory is never searched. A file Axiom found by looking around would
// not be evidence that the agent loaded it.
//
// Selection is not on its own a boundary, because a path can be made to lead
// somewhere else. A repository is not trusted input — it may have been cloned
// from anywhere, and CLAUDE.md may be a symlink to a private key — so every
// one of these paths is resolved inside the project root by os.Root, which
// refuses a link out of it. Where the bytes live is bounded by the root, and
// which bytes are looked at is bounded by this list.
const (
	projectInstructions  = "CLAUDE.md"
	projectSettings      = ".claude/settings.json"
	localProjectSettings = ".claude/settings.local.json"
	subagentDirectory    = ".claude/agents"
	definitionSuffix     = ".md"
)

var harnessFiles = []struct {
	kind event.HarnessKind
	path string
}{
	{event.HarnessProjectInstructions, projectInstructions},
	{event.HarnessProjectSettings, projectSettings},
	{event.HarnessLocalProjectSettings, localProjectSettings},
}

// ObserveHarness records what Axiom can see of the agent's project-local
// configuration, and does nothing for any event that is not a session start.
//
// It is the second half of hook-time ingestion: Ingest translates what the
// agent reported, and this adds the one thing the agent reports nothing about.
// Claude Code 2.1.228 was observed sending session_id, transcript_path, cwd,
// hook_event_name and source at SessionStart, and nothing describing its
// configuration or its own version, so configuration Axiom does not look at
// for itself is configuration no record can hold.
//
// It happens here, at the start, because provenance has to describe the
// session that ran. Reading these files while analyzing a log would describe
// the machine at the time of the report and attribute it to work recorded days
// earlier.
//
// It cannot fail. Every problem it meets is recorded as a component status, or
// leaves provenance unrecorded, and the session start is written either way.
func ObserveHarness(ev *event.Event) {
	if ev == nil || ev.Type != event.TypeSessionStart || ev.Session == nil {
		return
	}
	ev.Session.Harness = observeHarness(ev.Cwd)
}

// observeHarness observes the project the session was started in.
//
// A working directory the agent did not report, or reported as something other
// than an absolute path, resolves no project: there is nowhere to look, and
// looking relative to the hook process's own directory would record a project
// that has nothing to do with the session. A root that is not a directory
// resolves none either. All three record no provenance rather than a project
// full of absent components, which would be a claim about a project Axiom
// never found.
//
// Resolving the root walks up from the working directory looking for a
// repository, which asks whether a .git entry exists in each ancestor. It opens
// none of them and reads nothing there: the walk decides where the eligible
// paths hang from, and is not itself an observation.
//
// The root is then opened once, and every path below is resolved through that
// one open directory. That is what makes the root a boundary rather than a
// prefix: os.Root resolves each component itself and refuses any that leaves,
// so a repository cannot spend a symlink to have Axiom read somewhere else.
// A working directory whose root will not open as a directory records no
// provenance.
func observeHarness(cwd string) *event.Harness {
	if !filepath.IsAbs(cwd) {
		return nil
	}
	root, err := os.OpenRoot(project.Root(cwd))
	if err != nil {
		return nil
	}
	defer root.Close()

	components := make([]event.HarnessComponent, 0, len(harnessFiles)+1)
	for _, f := range harnessFiles {
		components = append(components, observeFile(root, f.kind, f.path))
	}
	return &event.Harness{Components: append(components, observeSubagents(root)...)}
}

// observeFile records the identity of one file.
//
// A symlink within the project is followed, because the agent reads through
// one too and a project that keeps CLAUDE.md in docs and links it into place
// has ordinary configuration, not none. A symlink that leaves the project is
// not followed and is not opened, and neither is an absolute one: the file it
// names is not the project's configuration, it is whatever the repository
// asked for.
//
// Nothing here re-checks a path it has already checked. The open is the check:
// os.Root resolves the whole path itself and refuses to leave the root, and
// what comes back is a descriptor, so the file that is measured is the file
// that was opened and not whatever the name means a moment later. It is opened
// without blocking and its type is read from the descriptor, so a pipe left in
// the repository is refused instead of holding the session start open.
//
// The path recorded is always the eligible path Axiom looked at, never what a
// link resolved to. Where the link led is not a fact about the project.
func observeFile(root *os.Root, kind event.HarnessKind, rel string) event.HarnessComponent {
	c := event.HarnessComponent{Kind: kind, Path: rel}

	f, err := root.OpenFile(filepath.FromSlash(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Nothing there, or a link leading nowhere. Neither is a file.
		c.Status = event.HarnessAbsent
		return c
	case err != nil:
		c.Status = refused(root, rel)
		return c
	}
	defer f.Close()

	d, ok := digestOpen(f)
	if !ok {
		c.Status = event.HarnessUnreadable
		return c
	}
	c.Status, c.Digest = event.HarnessObserved, d
	return c
}

// refused says what an open that failed for some reason other than absence
// established, which is very little, and never more than it can.
//
// A link that leaves the project is refused, and so is a link inside it whose
// target will not open. The two are not told apart: os.Root reports a refusal
// as an error like any other, and separating them would mean resolving the
// link to see where it went, which is the thing being avoided. What is left is
// what the record says — a link Axiom did not read through — and it is only
// said of a path that is in fact a link.
func refused(root *os.Root, rel string) event.HarnessStatus {
	info, err := root.Lstat(filepath.FromSlash(rel))
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return event.HarnessNotFollowed
	}
	return event.HarnessUnreadable
}

// digestOpen hashes an open file's exact bytes.
//
// It works on the descriptor and never on the name again. The type is checked
// here because a directory or a device opened under an eligible name is not a
// configuration file, and the size is bounded by what is read rather than by
// what a stat reported, so a file that grows while being read is refused
// rather than half-read.
//
// Nothing about a failure is carried out of here: an error from a
// configuration file can quote the configuration.
func digestOpen(f *os.File) (string, bool) {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}

	content, err := io.ReadAll(io.LimitReader(f, MaxComponentBytes+1))
	if err != nil || len(content) > MaxComponentBytes {
		return "", false
	}
	return digest.HarnessFile(content), true
}

// observeSubagents enumerates the project's subagent definitions.
//
// The directory itself is always recorded, ahead of whatever it held, so that
// a project with no definitions is distinguishable from a directory Axiom
// could not read and from an Axiom that did not enumerate definitions at all.
//
// Enumeration is one level deep and covers definition files only. A nested
// directory is not descended into: a recursive read of a directory the user
// controls is a different contract, with different costs and a different
// privacy question, and this one deliberately stops short of it. Names are
// sorted so that two observations of one directory cannot disagree because the
// filesystem returned entries in another order.
//
// The directory is subject to the same boundary as the files, and has to be:
// a directory symlink is the one that would matter, because following one out
// of the project would turn a fixed list of four paths into a listing of
// somewhere else, and then a read of everything named .md in it. It is opened
// through the root, which refuses that, and the entries it lists are read from
// that open directory. Each definition is then observed like any other file,
// so a link among the entries is bounded too.
func observeSubagents(root *os.Root) []event.HarnessComponent {
	c := event.HarnessComponent{Kind: event.HarnessSubagentDirectory, Path: subagentDirectory}
	only := func(s event.HarnessStatus) []event.HarnessComponent {
		c.Status = s
		return []event.HarnessComponent{c}
	}

	d, err := root.OpenFile(filepath.FromSlash(subagentDirectory), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return only(event.HarnessAbsent)
	case err != nil:
		return only(refused(root, subagentDirectory))
	}
	defer d.Close()

	// A name that is not a directory at all lists nothing, and is reported
	// as a directory whose contents were not established.
	entries, err := d.ReadDir(-1)
	if err != nil {
		return only(event.HarnessUnreadable)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), definitionSuffix) {
			continue
		}
		names = append(names, e.Name())
	}
	// A directory holding more than Axiom will read is reported as one it
	// did not establish. Listing the first few would present part of a set
	// as the set.
	if len(names) > MaxSubagentDefinitions {
		return only(event.HarnessUnreadable)
	}
	slices.Sort(names)

	c.Status = event.HarnessObserved
	out := make([]event.HarnessComponent, 0, len(names)+1)
	out = append(out, c)
	for _, name := range names {
		out = append(out, observeFile(root, event.HarnessSubagentDefinition, subagentDirectory+"/"+name))
	}
	return out
}
