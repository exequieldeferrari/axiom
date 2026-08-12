package claude_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/claude"
)

// seedInstalled returns a settings document with Axiom installed into it.
func seedInstalled(t *testing.T, existing []byte, telemetry bool) []byte {
	t.Helper()

	res, err := claude.Install(existing, exe)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !telemetry {
		return res.Content
	}
	res, err = claude.InstallTelemetry(res.Content, endpoint)
	if err != nil {
		t.Fatalf("InstallTelemetry: %v", err)
	}
	return res.Content
}

// An install followed by an uninstall leaves nothing of either behind.
func TestUninstallRemovesEverythingAnInstallWrote(t *testing.T) {
	t.Parallel()

	removal, err := claude.Uninstall(seedInstalled(t, nil, true))
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !removal.Changed() {
		t.Fatal("Changed() = false, want true")
	}
	if !slices.Equal(removal.Events, claude.HookEvents) {
		t.Errorf("Events = %v, want %v", removal.Events, claude.HookEvents)
	}
	if removal.Handlers != len(claude.HookEvents) {
		t.Errorf("Handlers = %d, want %d", removal.Handlers, len(claude.HookEvents))
	}
	if !removal.Telemetry {
		t.Error("Telemetry = false, want the configuration reported as removed")
	}
	if !removal.Empty {
		t.Errorf("Empty = false, want true for a document that held only Axiom:\n%s", removal.Content)
	}

	doc := decode(t, removal.Content)
	if len(doc) != 0 {
		t.Errorf("document still holds %d keys, want none:\n%s", len(doc), removal.Content)
	}
}

// Removing Axiom must not touch a single thing the user configured.
func TestUninstallPreservesUnrelatedSettings(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "permissions": {"allow": ["Bash(git *)"]},
  "tui": {"theme": "dark"},
  "cleanupPeriodDays": 30,
  "env": {"EDITOR": "vim", "OTEL_LOG_USER_PROMPTS": "0"},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/guard.sh"}]}
    ],
    "PostToolUse": [
      {"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "/usr/local/bin/lint.sh", "timeout": 30}]}
    ]
  }
}`)

	removal, err := claude.Uninstall(seedInstalled(t, existing, true))
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if removal.Empty {
		t.Error("Empty = true, want false for a document with the user's own settings")
	}

	content := string(removal.Content)
	doc := decode(t, removal.Content)
	if _, ok := doc["permissions"]; !ok {
		t.Error("permissions key was dropped")
	}
	if tui, ok := doc["tui"].(map[string]any); !ok || tui["theme"] != "dark" {
		t.Errorf("tui = %#v, want it preserved", doc["tui"])
	}
	// A number must survive as the literal the user wrote, not as 30.0.
	if !strings.Contains(content, `"cleanupPeriodDays": 30`) {
		t.Errorf("numeric literal was rewritten:\n%s", content)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		t.Fatalf("env = %#v, want the user's variables preserved", doc["env"])
	}
	if env["EDITOR"] != "vim" || env["OTEL_LOG_USER_PROMPTS"] != "0" {
		t.Errorf("env = %#v, want EDITOR and the user's own OTEL flag kept", env)
	}
	if len(env) != 2 {
		t.Errorf("env holds %d variables, want only the user's two:\n%s", len(env), content)
	}

	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks = %#v, want the user's hooks preserved", doc["hooks"])
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("the unrelated PreToolUse hook was removed")
	}
	if !strings.Contains(content, "guard.sh") || !strings.Contains(content, "lint.sh") {
		t.Errorf("unrelated hook commands were lost:\n%s", content)
	}
	if !strings.Contains(content, `"timeout": 30`) {
		t.Errorf("the unrelated hook's timeout was rewritten:\n%s", content)
	}
	if got := handlerCount(t, doc, "PostToolUse"); got != 1 {
		t.Errorf("PostToolUse has %d handlers, want only the user's own", got)
	}
	for _, name := range []string{"SessionStart", "PostToolUseFailure", "SessionEnd"} {
		if _, present := hooks[name]; present {
			t.Errorf("%s survived with no handlers left in it", name)
		}
	}
	if strings.Contains(content, exe) {
		t.Errorf("an Axiom handler survived:\n%s", content)
	}
}

// A user handler sharing one of Axiom's events, in Axiom's own group shape, is
// the case where a careless filter would take the wrong one out.
func TestUninstallKeepsAnotherHandlerInTheSameGroup(t *testing.T) {
	t.Parallel()

	seeded := []byte(`{
  "hooks": {
    "PostToolUse": [
      {"hooks": [
        {"type": "command", "command": "` + exe + `", "args": ["hook", "claude"], "timeout": 5},
        {"type": "command", "command": "/usr/local/bin/notify.sh"}
      ]}
    ]
  }
}`)

	removal, err := claude.Uninstall(seeded)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if removal.Handlers != 1 {
		t.Errorf("Handlers = %d, want 1", removal.Handlers)
	}

	doc := decode(t, removal.Content)
	if got := handlerCount(t, doc, "PostToolUse"); got != 1 {
		t.Errorf("PostToolUse has %d handlers, want the user's one", got)
	}
	if !strings.Contains(string(removal.Content), "notify.sh") {
		t.Errorf("the user's handler was removed:\n%s", removal.Content)
	}
}

// A handler left behind by a binary that has since moved is still Axiom's, and
// an uninstall the user asked for should not leave it firing.
func TestUninstallRemovesHandlersOfAnyPath(t *testing.T) {
	t.Parallel()

	seeded := []byte(`{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "/old/path/axiom", "args": ["hook", "claude"], "timeout": 5}]},
      {"hooks": [{"type": "command", "command": "` + exe + `", "args": ["hook", "claude"], "timeout": 5}]}
    ]
  }
}`)

	removal, err := claude.Uninstall(seeded)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if removal.Handlers != 2 {
		t.Errorf("Handlers = %d, want both", removal.Handlers)
	}
	if strings.Contains(string(removal.Content), "axiom") {
		t.Errorf("an Axiom handler survived:\n%s", removal.Content)
	}
}

// A command that merely looks similar is not Axiom's to remove.
func TestUninstallLeavesLookalikeHandlersAlone(t *testing.T) {
	t.Parallel()

	for name, handler := range map[string]string{
		"another tool called axiom":  `{"type": "command", "command": "/usr/local/bin/axiom", "args": ["profile"]}`,
		"a shell-form axiom command": `{"type": "command", "command": "/usr/local/bin/axiom hook claude"}`,
		"a different hook agent":     `{"type": "command", "command": "/usr/local/bin/axiom", "args": ["hook", "codex"]}`,
		"not a command hook":         `{"type": "http", "url": "http://localhost:8080/hooks", "args": ["hook", "claude"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			seeded := []byte(`{"hooks": {"PostToolUse": [{"hooks": [` + handler + `]}]}}`)
			removal, err := claude.Uninstall(seeded)
			if err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if removal.Changed() {
				t.Errorf("Changed() = true, want the handler left alone:\n%s", removal.Content)
			}
			if got := handlerCount(t, decode(t, removal.Content), "PostToolUse"); got != 1 {
				t.Errorf("PostToolUse has %d handlers, want the one that was there", got)
			}
		})
	}
}

func TestUninstallIsIdempotent(t *testing.T) {
	t.Parallel()

	first, err := claude.Uninstall(seedInstalled(t, []byte(`{"tui": {"theme": "dark"}}`), true))
	if err != nil {
		t.Fatalf("first uninstall: %v", err)
	}

	second, err := claude.Uninstall(first.Content)
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if second.Changed() {
		t.Error("Changed() = true on a document with no Axiom in it, want false")
	}
	if string(second.Content) != string(first.Content) {
		t.Fatalf("a second uninstall rewrote the document\nfirst:\n%s\nsecond:\n%s", first.Content, second.Content)
	}
}

// Nothing is added on the way through: a document with no hooks at all must not
// come back with the key.
func TestUninstallAddsNothing(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"empty document":  `{}`,
		"other settings":  `{"tui": {"theme": "dark"}}`,
		"empty hooks":     `{"hooks": {}}`,
		"unrelated hooks": `{"hooks": {"PreToolUse": [{"hooks": [{"type": "command", "command": "/bin/true"}]}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			removal, err := claude.Uninstall([]byte(data))
			if err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if removal.Changed() {
				t.Errorf("Changed() = true, want false")
			}
			doc := decode(t, removal.Content)
			if _, present := doc["env"]; present {
				t.Errorf("an env key appeared:\n%s", removal.Content)
			}
			if !strings.Contains(data, "hooks") {
				if _, present := doc["hooks"]; present {
					t.Errorf("a hooks key appeared:\n%s", removal.Content)
				}
			}
		})
	}
}

// A group with no handlers is somebody else's oddity, not Axiom's leftover, so
// it survives beside the group that is removed.
func TestUninstallKeepsGroupsItDidNotTouch(t *testing.T) {
	t.Parallel()

	seeded := []byte(`{
  "hooks": {
    "SessionStart": [
      {"matcher": "Bash"},
      {"matcher": "Edit", "hooks": []},
      {"hooks": [{"type": "command", "command": "` + exe + `", "args": ["hook", "claude"], "timeout": 5}]}
    ]
  }
}`)

	removal, err := claude.Uninstall(seeded)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if removal.Handlers != 1 {
		t.Errorf("Handlers = %d, want 1", removal.Handlers)
	}

	groups, ok := decode(t, removal.Content)["hooks"].(map[string]any)["SessionStart"].([]any)
	if !ok || len(groups) != 2 {
		t.Fatalf("SessionStart = %#v, want the two groups Axiom did not own", groups)
	}
	if groups[0].(map[string]any)["matcher"] != "Bash" || groups[1].(map[string]any)["matcher"] != "Edit" {
		t.Errorf("groups = %#v, want them in the order they were written", groups)
	}
}

func TestUninstallRefusesUnparseableSettings(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"comments":     "{\n  // axiom\n  \"hooks\": {}\n}",
		"truncated":    `{"hooks": {`,
		"not object":   `["hooks"]`,
		"bad hooks":    `{"hooks": "none"}`,
		"bad event":    `{"hooks": {"PostToolUse": "none"}}`,
		"bad group":    `{"hooks": {"PostToolUse": ["nope"]}}`,
		"bad handlers": `{"hooks": {"PostToolUse": [{"hooks": "nope"}]}}`,
		"bad env":      `{"env": "none"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := claude.Uninstall([]byte(data)); err == nil {
				t.Fatalf("Uninstall(%s) succeeded, want a refusal", data)
			}
		})
	}
}

// Telemetry is only Axiom's to remove when the whole block still holds what an
// install writes and points at a local receiver. Anything else may be
// somebody's own export pipeline.
//
// The remote cases are the ones that matter most: /v1/logs is the OTLP path
// every collector serves, so a team exporting Claude Code's logs to their own
// collector writes the same variables Axiom does, and only the address tells
// the two apart.
func TestUninstallLeavesForeignTelemetryAlone(t *testing.T) {
	t.Parallel()

	for name, env := range map[string]string{
		"a remote collector over plain http": `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://otel-collector.corp.internal:4318/v1/logs"}`,
		"a collector on the network":         `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://192.168.1.10:4318/v1/logs"}`,
		"a host that merely reads local":     `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://localhost.corp.example:4318/v1/logs"}`,
		"an endpoint with no port":           `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://localhost/v1/logs"}`,
		"another collector":                  `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "https://otel.corp.example/v1/logs"}`,
		"another path":                       `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://127.0.0.1:4318/v1/traces"}`,
		"another protocol":                   `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "` + endpoint + `"}`,
		"another exporter":                   `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "console", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "` + endpoint + `"}`,
		"telemetry off":                      `{"CLAUDE_CODE_ENABLE_TELEMETRY": "0", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "` + endpoint + `"}`,
		"no endpoint at all":                 `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "OTEL_LOGS_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json"}`,
		"enabled by the user":                `{"CLAUDE_CODE_ENABLE_TELEMETRY": "1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			removal, err := claude.Uninstall([]byte(`{"env": ` + env + `}`))
			if err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if removal.Telemetry {
				t.Errorf("Telemetry = true, want the configuration left alone:\n%s", removal.Content)
			}
			if removal.Changed() {
				t.Error("Changed() = true, want false")
			}

			before := decode(t, []byte(env))
			after, ok := decode(t, removal.Content)["env"].(map[string]any)
			if !ok {
				t.Fatalf("env was removed entirely:\n%s", removal.Content)
			}
			for key, want := range before {
				if after[key] != want {
					t.Errorf("env[%s] = %v, want %v", key, after[key], want)
				}
			}
		})
	}
}

// An install may have been given --addr, so the port is not what identifies the
// export as Axiom's, and a local receiver can be addressed several ways.
func TestUninstallRemovesTelemetryOnAnyLoopbackAddress(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:4318", "127.0.0.1:4319", "127.0.0.2:4318", "localhost:4318", "[::1]:4318"} {
		seeded, err := claude.InstallTelemetry(nil, claude.TelemetryEndpoint(addr))
		if err != nil {
			t.Fatalf("InstallTelemetry(%s): %v", addr, err)
		}
		removal, err := claude.Uninstall(seeded.Content)
		if err != nil {
			t.Fatalf("Uninstall(%s): %v", addr, err)
		}
		if !removal.Telemetry {
			t.Errorf("Telemetry = false for %s, want it removed:\n%s", addr, removal.Content)
		}
		if _, present := decode(t, removal.Content)["env"]; present {
			t.Errorf("an empty env object was left behind for %s:\n%s", addr, removal.Content)
		}
	}
}

// Somebody whose team exports Claude Code's logs to their own collector can
// still install Axiom's hooks, because an install only refuses the telemetry it
// was asked to write. Removing the hooks must not take their export with it.
func TestUninstallFileKeepsARemoteExportItNeverInstalled(t *testing.T) {
	t.Parallel()

	const remote = "http://otel-collector.corp.internal:4318/v1/logs"
	existing := []byte(`{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "` + remote + `"
  }
}`)

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, seedInstalled(t, existing, false), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	removal, err := claude.UninstallFile(path, false)
	if err != nil {
		t.Fatalf("UninstallFile: %v", err)
	}
	if removal.Telemetry {
		t.Error("Telemetry = true, want an export Axiom did not install left alone")
	}
	if removal.Handlers != len(claude.HookEvents) {
		t.Errorf("Handlers = %d, want the hooks removed", removal.Handlers)
	}
	if removal.Empty {
		t.Error("Empty = true, want the file reported as still holding the user's export")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	env, ok := decode(t, data)["env"].(map[string]any)
	if !ok {
		t.Fatalf("the user's export was removed:\n%s", data)
	}
	want := map[string]any{
		"CLAUDE_CODE_ENABLE_TELEMETRY":     "1",
		"OTEL_LOGS_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": remote,
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("env[%s] = %v, want %v", key, env[key], value)
		}
	}
	if len(env) != len(want) {
		t.Errorf("env holds %d variables, want the user's four:\n%s", len(env), data)
	}
	if _, present := decode(t, data)["hooks"]; present {
		t.Errorf("Axiom's hooks survived:\n%s", data)
	}
}

func TestUninstallFileLeavesNoFileWhereThereWasNone(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")

	removal, err := claude.UninstallFile(path, false)
	if err != nil {
		t.Fatalf("UninstallFile: %v", err)
	}
	if removal.Changed() {
		t.Error("Changed() = true, want false when there was nothing to remove")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall created %s", path)
	}
}

// The file stays, even with nothing left in it: Axiom does not know whether it
// created the file, and deleting one is not reversible.
func TestUninstallFileKeepsAnEmptiedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, seedInstalled(t, nil, true), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	removal, err := claude.UninstallFile(path, false)
	if err != nil {
		t.Fatalf("UninstallFile: %v", err)
	}
	if !removal.Empty {
		t.Error("Empty = false, want the caller told the document is now empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if len(decode(t, data)) != 0 {
		t.Errorf("settings still hold configuration:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600 preserved", perm)
	}
}

// An empty file is a settings file Claude Code or an interrupted write may have
// left; there is nothing of Axiom's in it and nothing to do about it.
func TestUninstallFileAcceptsAnEmptyFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	removal, err := claude.UninstallFile(path, false)
	if err != nil {
		t.Fatalf("UninstallFile: %v", err)
	}
	if removal.Changed() {
		t.Error("Changed() = true, want false")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(after) != "\n" {
		t.Fatalf("the file was rewritten: %q", after)
	}
}

func TestUninstallFileDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	seeded := seedInstalled(t, nil, true)
	if err := os.WriteFile(path, seeded, 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	removal, err := claude.UninstallFile(path, true)
	if err != nil {
		t.Fatalf("UninstallFile: %v", err)
	}
	if !removal.Changed() {
		t.Error("Changed() = false, want the caller told a write would happen")
	}
	if len(removal.Content) == 0 {
		t.Error("Content is empty, want the document that would be written")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(after) != string(seeded) {
		t.Fatalf("the dry run modified the file:\n%s", after)
	}
}

// A file with nothing of Axiom's in it is not rewritten at all, so an uninstall
// cannot reformat somebody's settings as a side effect.
func TestUninstallFileLeavesAnUnrelatedFileByteForByte(t *testing.T) {
	t.Parallel()

	seeded := []byte("{\n\t\"tui\": {\"theme\": \"dark\"},\n\t\"cleanupPeriodDays\": 30\n}\n")
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, seeded, 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	removal, err := claude.UninstallFile(path, false)
	if err != nil {
		t.Fatalf("UninstallFile: %v", err)
	}
	if removal.Changed() {
		t.Error("Changed() = true, want false")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(after) != string(seeded) {
		t.Fatalf("the file was rewritten:\nbefore:\n%s\nafter:\n%s", seeded, after)
	}
}

func TestUninstallFileRefusesUnparseableSettings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	seeded := []byte(`{"hooks": {`)
	if err := os.WriteFile(path, seeded, 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if _, err := claude.UninstallFile(path, false); err == nil {
		t.Fatal("UninstallFile succeeded, want a refusal")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(after) != string(seeded) {
		t.Fatalf("settings were modified despite the refusal:\n%s", after)
	}
}
