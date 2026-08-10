package claude_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/claude"
)

const exe = "/usr/local/bin/axiom"

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, data)
	}
	return doc
}

// handlerCount reports how many handlers are configured for an event.
func handlerCount(t *testing.T, doc map[string]any, eventName string) int {
	t.Helper()

	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks is not an object: %#v", doc["hooks"])
	}
	groups, ok := hooks[eventName].([]any)
	if !ok {
		return 0
	}

	count := 0
	for _, g := range groups {
		group := g.(map[string]any)
		list, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		count += len(list)
	}
	return count
}

func TestInstallIntoEmptySettings(t *testing.T) {
	t.Parallel()

	res, err := claude.Install(nil, exe)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true for a fresh install")
	}

	doc := decode(t, res.Content)
	for _, name := range claude.HookEvents {
		if got := handlerCount(t, doc, name); got != 1 {
			t.Errorf("%s has %d handlers, want 1", name, got)
		}
	}
}

func TestInstalledHandlerShape(t *testing.T) {
	t.Parallel()

	res, err := claude.Install(nil, exe)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	doc := decode(t, res.Content)
	groups := doc["hooks"].(map[string]any)["PostToolUse"].([]any)
	handler := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)

	if handler["type"] != "command" {
		t.Errorf("type = %v, want command", handler["type"])
	}
	if handler["command"] != exe {
		t.Errorf("command = %v, want %s", handler["command"], exe)
	}
	args, ok := handler["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "hook" || args[1] != "claude" {
		t.Errorf("args = %#v, want [hook claude]", handler["args"])
	}
	// A matcher is deliberately absent: an omitted matcher matches every tool.
	if _, present := groups[0].(map[string]any)["matcher"]; present {
		t.Error("matcher is set, want it omitted so every tool matches")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	t.Parallel()

	first, err := claude.Install(nil, exe)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	second, err := claude.Install(first.Content, exe)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if second.Changed {
		t.Error("Changed = true on reinstall, want false")
	}
	if string(second.Content) != string(first.Content) {
		t.Fatalf("reinstall rewrote the document\nfirst:\n%s\nsecond:\n%s", first.Content, second.Content)
	}

	doc := decode(t, second.Content)
	for _, name := range claude.HookEvents {
		if got := handlerCount(t, doc, name); got != 1 {
			t.Errorf("%s has %d handlers after reinstall, want 1", name, got)
		}
	}
}

func TestInstallPreservesExistingConfiguration(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "permissions": {"allow": ["Bash(git *)"]},
  "tui": {"theme": "dark"},
  "cleanupPeriodDays": 30,
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/guard.sh"}]}
    ],
    "PostToolUse": [
      {"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "/usr/local/bin/lint.sh", "timeout": 30}]}
    ]
  }
}`)

	res, err := claude.Install(existing, exe)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	doc := decode(t, res.Content)
	if _, ok := doc["permissions"]; !ok {
		t.Error("permissions key was dropped")
	}
	if tui, ok := doc["tui"].(map[string]any); !ok || tui["theme"] != "dark" {
		t.Errorf("tui = %#v, want it preserved", doc["tui"])
	}
	// A number must survive as the literal the user wrote, not as 30.0 or 3e+01.
	if !strings.Contains(string(res.Content), `"cleanupPeriodDays": 30`) {
		t.Errorf("numeric literal was rewritten:\n%s", res.Content)
	}

	hooks := doc["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("the unrelated PreToolUse hook was removed")
	}
	if !strings.Contains(string(res.Content), "guard.sh") || !strings.Contains(string(res.Content), "lint.sh") {
		t.Errorf("unrelated hook commands were lost:\n%s", res.Content)
	}
	// The user's own PostToolUse hook plus Axiom's.
	if got := handlerCount(t, doc, "PostToolUse"); got != 2 {
		t.Errorf("PostToolUse has %d handlers, want 2", got)
	}
}

func TestInstallRefusesWhenAxiomPointsElsewhere(t *testing.T) {
	t.Parallel()

	installed, err := claude.Install(nil, "/old/path/axiom")
	if err != nil {
		t.Fatalf("seed install: %v", err)
	}

	_, err = claude.Install(installed.Content, exe)
	var conflict *claude.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want a ConflictError", err)
	}
	if len(conflict.ExistingPaths) != 1 || conflict.ExistingPaths[0] != "/old/path/axiom" || conflict.WantPath != exe {
		t.Errorf("conflict = %+v", conflict)
	}
}

// A stale handler ordered after the current one would otherwise go unnoticed
// and make Claude Code record every event twice.
func TestInstallRefusesDuplicateAxiomHandlers(t *testing.T) {
	t.Parallel()

	seeded := []byte(`{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "` + exe + `", "args": ["hook", "claude"], "timeout": 5}]},
      {"hooks": [{"type": "command", "command": "/stale/path/axiom", "args": ["hook", "claude"], "timeout": 5}]}
    ]
  }
}`)

	_, err := claude.Install(seeded, exe)
	var conflict *claude.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want a ConflictError", err)
	}
	if len(conflict.ExistingPaths) != 2 {
		t.Errorf("ExistingPaths = %v, want both handlers", conflict.ExistingPaths)
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error = %q, want it to explain the duplicate", err)
	}
}

// A conflict on a later event must not leave earlier events half-installed.
func TestConflictLeavesFileUntouched(t *testing.T) {
	t.Parallel()

	seeded := []byte(`{
  "hooks": {
    "SessionEnd": [
      {"hooks": [{"type": "command", "command": "/old/path/axiom", "args": ["hook", "claude"], "timeout": 5}]}
    ]
  }
}`)

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, seeded, 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if _, err := claude.InstallFile(path, exe, false); err == nil {
		t.Fatal("InstallFile succeeded, want a conflict")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(after) != string(seeded) {
		t.Fatalf("settings were modified despite the conflict:\n%s", after)
	}
}

func TestInstallRefusesUnparseableSettings(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"comments":   "{\n  // axiom\n  \"hooks\": {}\n}",
		"truncated":  `{"hooks": {`,
		"not object": `["hooks"]`,
		"bad hooks":  `{"hooks": "none"}`,
		"bad event":  `{"hooks": {"PostToolUse": "none"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := claude.Install([]byte(data), exe); err == nil {
				t.Fatalf("Install(%s) succeeded, want a refusal", data)
			}
		})
	}
}

func TestInstallFilePreservesFileMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"permissions":{}}`), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if _, err := claude.InstallFile(path, exe, false); err != nil {
		t.Fatalf("InstallFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

// Dotfile managers symlink settings.json into a tracked repository. Renaming
// onto the link would replace it with a regular file and silently detach the
// user's configuration from that repository.
func TestInstallFileWritesThroughSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create dotfiles directory: %v", err)
	}
	if err := os.WriteFile(target, []byte(`{"permissions":{}}`), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := claude.InstallFile(link, exe, false); err != nil {
		t.Fatalf("InstallFile: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced with a regular file")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if got := handlerCount(t, decode(t, data), "SessionStart"); got != 1 {
		t.Errorf("the symlink target has %d SessionStart handlers, want 1", got)
	}
}

func TestInstallFileCreatesMissingSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.local.json")

	res, err := claude.InstallFile(path, exe, false)
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if got := handlerCount(t, decode(t, data), "SessionStart"); got != 1 {
		t.Errorf("SessionStart has %d handlers, want 1", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("new file mode = %o, want 600", perm)
	}
}

func TestInstallFileDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	res, err := claude.InstallFile(path, exe, true)
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true so the caller knows a write would happen")
	}
	if len(res.Content) == 0 {
		t.Error("Content is empty, want the document that would be written")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created %s", path)
	}
}

func TestInstallFileIdempotentOnDisk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if _, err := claude.InstallFile(path, exe, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	res, err := claude.InstallFile(path, exe, false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true on reinstall, want false")
	}

	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("reinstall changed the file on disk\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
