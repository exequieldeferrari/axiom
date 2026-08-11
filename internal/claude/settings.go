package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// HookEvents are the Claude Code hook events Axiom installs.
var HookEvents = []string{
	eventSessionStart,
	eventPostToolUse,
	eventPostToolUseFailed,
	eventSessionEnd,
}

const (
	hookSubcommand = "hook"
	hookAgentArg   = "claude"

	// hookTimeoutSeconds is generous for an append that takes milliseconds.
	// Note that a timeout on SessionEnd also raises Claude Code's own
	// end-of-session budget, which defaults to 1.5 seconds.
	hookTimeoutSeconds = 5

	defaultFileMode = fs.FileMode(0o600)
)

// Result reports the outcome of an install.
type Result struct {
	// Changed is false when the settings already contained Axiom's hooks.
	Changed bool
	// Content is the resulting settings document.
	Content []byte
}

// ConflictError reports existing Axiom hooks that Axiom will not rewrite:
// either one pointing at a different binary, which may be a deliberate second
// installation, or several for the same event, which would make Claude Code
// record everything more than once.
type ConflictError struct {
	Event         string
	ExistingPaths []string
	WantPath      string
}

func (e *ConflictError) Error() string {
	if len(e.ExistingPaths) > 1 {
		return fmt.Sprintf(
			"the %s hook already runs axiom %d times (%s), which would record every event more than once",
			e.Event, len(e.ExistingPaths), strings.Join(e.ExistingPaths, ", "))
	}
	return fmt.Sprintf(
		"the %s hook already runs a different axiom binary (%s, wanted %s)",
		e.Event, e.ExistingPaths[0], e.WantPath)
}

// Install merges Axiom's hook handlers into a Claude Code settings document.
//
// Unknown keys, unrelated hooks, and numeric literals are preserved. The
// document is only ever added to.
func Install(data []byte, exePath string) (Result, error) {
	doc, err := decodeDocument(data)
	if err != nil {
		return Result{}, err
	}

	hooks, err := hooksObject(doc)
	if err != nil {
		return Result{}, err
	}

	// Decide everything before mutating, so a conflict on the last event
	// cannot leave the first events half-installed.
	type decision struct {
		name   string
		groups []any
		add    bool
	}
	decisions := make([]decision, 0, len(HookEvents))
	for _, name := range HookEvents {
		groups, err := eventGroups(hooks, name)
		if err != nil {
			return Result{}, err
		}
		existing, err := findAxiomHandlers(groups)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", name, err)
		}
		if len(existing) > 1 || (len(existing) == 1 && existing[0] != exePath) {
			return Result{}, &ConflictError{Event: name, ExistingPaths: existing, WantPath: exePath}
		}
		decisions = append(decisions, decision{name: name, groups: groups, add: len(existing) == 0})
	}

	changed := false
	for _, d := range decisions {
		if !d.add {
			continue
		}
		hooks[d.name] = append(d.groups, map[string]any{
			"hooks": []any{newHandler(exePath)},
		})
		changed = true
	}
	doc["hooks"] = hooks

	content, err := marshalDocument(doc)
	if err != nil {
		return Result{}, err
	}
	return Result{Changed: changed, Content: content}, nil
}

// InstallOptions selects what an install writes.
type InstallOptions struct {
	// ExePath is the binary Claude Code should run for hook events.
	ExePath string
	// TelemetryEndpoint additionally configures Claude Code to export its
	// log records there. Empty leaves telemetry configuration alone, which is
	// the default: hooks are passive, while telemetry configuration changes
	// where a user's data goes and has to be asked for.
	TelemetryEndpoint string
	// DryRun returns the resulting document without writing it.
	DryRun bool
}

// InstallFile applies Install to a settings file on disk. With DryRun set,
// nothing is written and the resulting document is returned for inspection.
//
// Hooks and telemetry are merged into one document and written once, so a
// dry run shows exactly what a real run would produce.
func InstallFile(path string, opts InstallOptions) (Result, error) {
	data, mode, err := readSettings(path)
	if err != nil {
		return Result{}, err
	}

	res, err := Install(data, opts.ExePath)
	if err != nil {
		return Result{}, err
	}
	if opts.TelemetryEndpoint != "" {
		tel, err := InstallTelemetry(res.Content, opts.TelemetryEndpoint)
		if err != nil {
			return Result{}, err
		}
		res = Result{Changed: res.Changed || tel.Changed, Content: tel.Content}
	}
	if opts.DryRun || !res.Changed {
		return res, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, fmt.Errorf("create settings directory: %w", err)
	}
	if err := writeFileAtomic(path, res.Content, mode); err != nil {
		return Result{}, err
	}
	return res, nil
}

func readSettings(path string) ([]byte, fs.FileMode, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, defaultFileMode, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read settings: %w", err)
	}

	mode := defaultFileMode
	if info, err := os.Stat(path); err == nil {
		// Claude Code writes this file 0600; widening it would be a regression.
		mode = info.Mode().Perm()
	}
	return data, mode, nil
}

func decodeDocument(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	// Keeps numeric literals byte-identical instead of routing them through
	// float64, which would rewrite values the user did not ask us to touch.
	dec.UseNumber()

	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("settings file is not valid JSON, refusing to modify it: %w", err)
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	return doc, nil
}

func marshalDocument(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return buf.Bytes(), nil
}

func hooksObject(doc map[string]any) (map[string]any, error) {
	switch v := doc["hooks"].(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	default:
		return nil, errors.New(`existing "hooks" value is not an object, refusing to modify it`)
	}
}

func eventGroups(hooks map[string]any, name string) ([]any, error) {
	switch v := hooks[name].(type) {
	case nil:
		return nil, nil
	case []any:
		return v, nil
	default:
		return nil, fmt.Errorf("existing %q hook configuration is not an array, refusing to modify it", name)
	}
}

// findAxiomHandlers reports the executable path of every Axiom handler in
// groups. All of them are collected rather than the first, because a stale
// handler ordered after a current one is exactly the case that would otherwise
// install a silent duplicate.
func findAxiomHandlers(groups []any) ([]string, error) {
	var paths []string
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			return nil, errors.New("hook entry is not an object")
		}
		handlers, ok := group["hooks"]
		if !ok || handlers == nil {
			continue
		}
		list, ok := handlers.([]any)
		if !ok {
			return nil, errors.New(`hook entry "hooks" is not an array`)
		}
		for _, h := range list {
			handler, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := axiomHandlerPath(handler); ok {
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

// axiomHandlerPath recognises a handler by its argument vector rather than by
// its executable path, so a handler left behind by a binary that has since
// moved is reported as a conflict instead of being installed a second time.
func axiomHandlerPath(handler map[string]any) (string, bool) {
	if handler["type"] != "command" {
		return "", false
	}
	args, ok := handler["args"].([]any)
	if !ok || len(args) != 2 {
		return "", false
	}
	if args[0] != hookSubcommand || args[1] != hookAgentArg {
		return "", false
	}
	path, _ := handler["command"].(string)
	return path, true
}

// newHandler builds an exec-form handler. Exec form passes the executable path
// as a single argument with no shell involved, so a path containing spaces or
// shell metacharacters cannot be re-interpreted.
func newHandler(exePath string) map[string]any {
	return map[string]any{
		"type":    "command",
		"command": exePath,
		"args":    []any{hookSubcommand, hookAgentArg},
		"timeout": hookTimeoutSeconds,
	}
}

// writeFileAtomic replaces path in one step, so an interrupted write cannot
// leave a user's settings truncated.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	// Renaming onto a symlink replaces the link itself, which would detach a
	// settings file managed from a dotfiles repository. EvalSymlinks fails when
	// the file does not exist yet, and the original path is then the one to
	// create.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".axiom-settings-*")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set settings permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write settings: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close settings: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}
