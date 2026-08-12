package claude

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Removal reports what an uninstall took out of a settings document.
type Removal struct {
	// Events names the hook events Axiom handlers were removed from.
	Events []string
	// Handlers is how many handlers were removed across those events.
	Handlers int
	// Telemetry reports whether Axiom's telemetry configuration was removed.
	Telemetry bool
	// Empty reports that what remains holds no settings at all. The file is
	// still left in place; this exists so a user can be told they may delete
	// it.
	Empty bool
	// Content is the resulting settings document.
	Content []byte
}

// Changed reports whether anything of Axiom's was found to remove.
func (r Removal) Changed() bool { return len(r.Events) > 0 || r.Telemetry }

// Uninstall removes Axiom's own entries from a Claude Code settings document.
//
// Only what an install writes is taken out. Unrelated hooks keep their place
// and their order, unrelated settings are preserved as they were parsed, and a
// document that cannot be parsed is refused rather than rewritten — the same
// rules an install follows, because an uninstall is editing the same file for
// the same person.
func Uninstall(data []byte) (Removal, error) {
	doc, err := decodeDocument(data)
	if err != nil {
		return Removal{}, err
	}

	removal, err := removeHooks(doc)
	if err != nil {
		return Removal{}, err
	}
	telemetry, err := removeTelemetry(doc)
	if err != nil {
		return Removal{}, err
	}
	removal.Telemetry = telemetry
	removal.Empty = len(doc) == 0

	content, err := marshalDocument(doc)
	if err != nil {
		return Removal{}, err
	}
	removal.Content = content
	return removal, nil
}

// UninstallFile applies Uninstall to a settings file on disk. With dryRun set,
// nothing is written and the resulting document is returned for inspection.
//
// A file that does not exist is not an error. The job of an uninstall is to
// leave nothing of Axiom behind, and a missing file already satisfies that.
//
// The file is never deleted, even when nothing but an empty document remains.
// Axiom does not know whether it created the file, and an empty settings file
// is inert where a deleted one may have been a symlink into someone's dotfiles
// or a path another tool expects to exist. The removal reports that the
// document is empty so the choice to delete it stays the user's.
func UninstallFile(path string, dryRun bool) (Removal, error) {
	data, mode, err := readSettings(path)
	if err != nil {
		return Removal{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Removal{}, nil
	}

	removal, err := Uninstall(data)
	if err != nil {
		return Removal{}, err
	}
	if dryRun || !removal.Changed() {
		return removal, nil
	}
	if err := writeFileAtomic(path, removal.Content, mode); err != nil {
		return Removal{}, err
	}
	return removal, nil
}

// removeHooks takes Axiom's handlers out of every event it installs into.
//
// An event left with no handlers loses its key, and hooks left with no events
// loses its own, so an uninstall does not leave the shape of a configuration
// that is no longer there. Nothing is added: a document with no hooks at all
// does not acquire the key on the way through.
func removeHooks(doc map[string]any) (Removal, error) {
	var removal Removal

	hooks, err := hooksObject(doc)
	if err != nil {
		return Removal{}, err
	}
	if len(hooks) == 0 {
		return removal, nil
	}

	for _, name := range HookEvents {
		groups, err := eventGroups(hooks, name)
		if err != nil {
			return Removal{}, err
		}
		kept, removed, err := stripAxiomHandlers(groups)
		if err != nil {
			return Removal{}, fmt.Errorf("%s: %w", name, err)
		}
		if removed == 0 {
			continue
		}
		removal.Events = append(removal.Events, name)
		removal.Handlers += removed
		if len(kept) == 0 {
			delete(hooks, name)
			continue
		}
		hooks[name] = kept
	}

	if removal.Handlers > 0 && len(hooks) == 0 {
		delete(doc, "hooks")
	}
	return removal, nil
}

// stripAxiomHandlers removes Axiom's handlers from the groups of one event and
// reports the groups that remain.
//
// Handlers are recognized by their argument vector, exactly as an install
// recognizes them, so a handler left behind by a binary that has since moved is
// removed too: the user asked for Axiom to be gone, not for one path to be.
//
// A group emptied by the removal is dropped rather than left with an empty
// handler list. Such a group only ever existed to run Axiom, and a matcher with
// nothing behind it is configuration for nothing. A group Axiom did not touch
// is passed through as it was, including one that already had no handlers.
func stripAxiomHandlers(groups []any) ([]any, int, error) {
	kept := make([]any, 0, len(groups))
	removed := 0

	for _, entry := range groups {
		group, ok := entry.(map[string]any)
		if !ok {
			return nil, 0, errors.New("hook entry is not an object")
		}
		handlers, present := group["hooks"]
		if !present || handlers == nil {
			kept = append(kept, entry)
			continue
		}
		list, ok := handlers.([]any)
		if !ok {
			return nil, 0, errors.New(`hook entry "hooks" is not an array`)
		}

		keptHandlers := make([]any, 0, len(list))
		for _, h := range list {
			if handler, ok := h.(map[string]any); ok {
				if _, isAxiom := axiomHandlerPath(handler); isAxiom {
					removed++
					continue
				}
			}
			keptHandlers = append(keptHandlers, h)
		}

		switch {
		case len(keptHandlers) == len(list):
			kept = append(kept, entry)
		case len(keptHandlers) > 0:
			group["hooks"] = keptHandlers
			kept = append(kept, group)
		}
	}
	return kept, removed, nil
}

// removeTelemetry takes out the four variables an install writes, and only when
// three still hold the exact values an install writes and the fourth points at
// a local receiver.
//
// Any difference means the export is not Axiom's to remove. Someone may have
// pointed Claude Code at their own collector, and an uninstall that silently
// turned that off would do more damage than leaving four variables behind. So
// this recognizes an installed export, not an owned one — nothing records which
// tool wrote a variable — and every case it cannot recognize keeps its
// configuration.
func removeTelemetry(doc map[string]any) (bool, error) {
	env, err := envObject(doc)
	if err != nil {
		return false, err
	}
	if len(env) == 0 {
		return false, nil
	}

	for key, want := range map[string]string{
		envEnableTelemetry: "1",
		envLogsExporter:    "otlp",
		envLogsProtocol:    "http/json",
	} {
		if existing, ok := envValue(env, key); !ok || existing != want {
			return false, nil
		}
	}
	if endpoint, ok := envValue(env, envLogsEndpoint); !ok || !isReceiverEndpoint(endpoint) {
		return false, nil
	}

	for _, key := range []string{envEnableTelemetry, envLogsExporter, envLogsProtocol, envLogsEndpoint} {
		delete(env, key)
	}
	if len(env) == 0 {
		delete(doc, "env")
	}
	return true, nil
}

// isReceiverEndpoint reports whether a logs endpoint is one an axiom receiver
// would have been configured with: plain HTTP to the OTLP logs path, at a
// loopback address.
//
// The host has to be loopback because /v1/logs identifies nothing. It is the
// OTLP path every collector serves, so an endpoint is only recognizable as
// Axiom's by where it points, and an axiom receiver listens on the machine it
// runs on. Without that, a team exporting to their own collector over plain
// HTTP writes the same four variables Axiom does, and an uninstall would turn
// off an export Axiom never set up — one an install would have refused to
// touch, since it requires the endpoint to be exactly its own.
//
// The port is deliberately not compared: an install may have been given --addr,
// and which port somebody chose is not the question. An install given a
// non-loopback --addr is the case this gives up on, and it gives up by leaving
// the variables behind, which is a leftover rather than a loss.
func isReceiverEndpoint(endpoint string) bool {
	rest, ok := strings.CutPrefix(endpoint, "http://")
	if !ok {
		return false
	}
	addr, ok := strings.CutSuffix(rest, "/v1/logs")
	if !ok {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}
