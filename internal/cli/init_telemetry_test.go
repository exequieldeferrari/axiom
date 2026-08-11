package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settingsEnv reads the environment block Claude Code would apply.
func settingsEnv(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings are not valid JSON: %v\n%s", err, data)
	}
	return doc.Env
}

func initTelemetry(t *testing.T, opts initOptions) (string, string) {
	t.Helper()

	project := t.TempDir()
	t.Chdir(project)

	opts.telemetry = true
	if opts.addr == "" {
		opts.addr = DefaultAddr
	}

	var stdout bytes.Buffer
	if err := runInstall(opts, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	return filepath.Join(project, ".claude", "settings.local.json"), stdout.String()
}

func TestInitTelemetryConfiguresTheLogsSignalOnly(t *testing.T) {
	settings, out := initTelemetry(t, initOptions{})

	env := settingsEnv(t, settings)
	want := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":     "1",
		"OTEL_LOGS_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://" + DefaultAddr + "/v1/logs",
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("%s = %q, want %q", key, env[key], value)
		}
	}
	if len(env) != len(want) {
		t.Errorf("env has %d variables, want %d: %v", len(env), len(want), env)
	}
	if !strings.Contains(out, "axiom observe") {
		t.Errorf("output does not say how to record the telemetry:\n%s", out)
	}
}

// Telemetry configuration changes where a user's data goes, so it is opt-in.
func TestInitWithoutTelemetryLeavesTheEnvironmentAlone(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	var stdout bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(project, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(data), "OTEL") || strings.Contains(string(data), "env") {
		t.Errorf("plain init configured telemetry:\n%s", data)
	}
	if strings.Contains(stdout.String(), "telemetry") {
		t.Errorf("plain init mentions telemetry:\n%s", stdout.String())
	}
}

func TestInitTelemetryIsIdempotent(t *testing.T) {
	settings, _ := initTelemetry(t, initOptions{})

	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var stdout bytes.Buffer
	if err := runInstall(initOptions{telemetry: true, addr: DefaultAddr}, testExe, &stdout); err != nil {
		t.Fatalf("second runInstall: %v", err)
	}

	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("reinstall rewrote the settings:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(stdout.String(), "already installed") {
		t.Errorf("output does not report the no-op:\n%s", stdout.String())
	}
}

func TestInitTelemetryPreservesUnrelatedSettings(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	dir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	settings := filepath.Join(dir, "settings.local.json")
	existing := `{
  "model": "opus",
  "env": {"EDITOR": "vim"},
  "permissions": {"allow": ["Bash(git:*)"]}
}`
	if err := os.WriteFile(settings, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	var stdout bytes.Buffer
	if err := runInstall(initOptions{telemetry: true, addr: DefaultAddr}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["model"] != "opus" {
		t.Errorf("model = %v", doc["model"])
	}
	if _, ok := doc["permissions"]; !ok {
		t.Error("permissions were dropped")
	}
	if settingsEnv(t, settings)["EDITOR"] != "vim" {
		t.Errorf("unrelated environment variables changed: %v", settingsEnv(t, settings))
	}
}

// Someone already exporting to a collector keeps their configuration, and
// their settings file is left untouched.
func TestInitTelemetryRefusesToHijackAnExistingCollector(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	dir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	settings := filepath.Join(dir, "settings.local.json")
	existing := `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://collector.corp:4318"}}`
	if err := os.WriteFile(settings, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	var stdout bytes.Buffer
	err := runInstall(initOptions{telemetry: true, addr: DefaultAddr}, testExe, &stdout)
	if err == nil {
		t.Fatal("runInstall succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Errorf("the error does not name the conflict: %v", err)
	}

	data, err2 := os.ReadFile(settings)
	if err2 != nil {
		t.Fatalf("read settings: %v", err2)
	}
	if string(data) != existing {
		t.Errorf("the settings file was modified:\n%s", data)
	}
}

// A refusal must not install the hooks either, so that one command has one
// outcome.
func TestARefusedTelemetryInstallWritesNoHooks(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	dir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	settings := filepath.Join(dir, "settings.local.json")
	if err := os.WriteFile(settings, []byte(`{"env":{"OTEL_LOGS_EXPORTER":"console"}}`), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	var stdout bytes.Buffer
	if err := runInstall(initOptions{telemetry: true, addr: DefaultAddr}, testExe, &stdout); err == nil {
		t.Fatal("runInstall succeeded, want a refusal")
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(data), "hooks") {
		t.Errorf("hooks were installed by a refused command:\n%s", data)
	}
}

func TestInitTelemetryDryRunWritesNothing(t *testing.T) {
	settings, out := initTelemetry(t, initOptions{dryRun: true})

	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", settings)
	}
	// The dry run shows one document, containing both halves of the install.
	for _, want := range []string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "hooks", "SessionStart"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output omits %q:\n%s", want, out)
		}
	}
}

func TestInitTelemetryHonoursACustomAddress(t *testing.T) {
	settings, _ := initTelemetry(t, initOptions{addr: "127.0.0.1:4319"})

	if got := settingsEnv(t, settings)["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"]; got != "http://127.0.0.1:4319/v1/logs" {
		t.Errorf("endpoint = %q", got)
	}
}

// The flags that export prompts, responses and tool content are never written.
func TestInitTelemetryNeverEnablesContentLogging(t *testing.T) {
	settings, _ := initTelemetry(t, initOptions{})

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	for _, flag := range []string{
		"OTEL_LOG_USER_PROMPTS",
		"OTEL_LOG_ASSISTANT_RESPONSES",
		"OTEL_LOG_TOOL_DETAILS",
		"OTEL_LOG_TOOL_CONTENT",
		"OTEL_LOG_RAW_API_BODIES",
	} {
		if strings.Contains(string(data), flag) {
			t.Errorf("settings enable %s:\n%s", flag, data)
		}
	}
}
