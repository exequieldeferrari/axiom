// Package store persists canonical records to local append-only JSONL files.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/exequieldeferrari/axiom/internal/event"
)

const (
	// EventsFile records what an agent did, written by hook processes.
	EventsFile = "events.jsonl"
	// UsageFile records what an agent consumed, written by the receiver.
	// The two streams are kept apart because they have different writers and
	// different lifetimes: a fault in one cannot corrupt the other.
	UsageFile = "usage.jsonl"
)

// MaxRecordBytes bounds a single serialized record. Records stay small because
// Axiom stores metadata rather than content, which also keeps every append
// within one write syscall.
const MaxRecordBytes = 64 << 10

// Record is a canonical record that carries the schema it was written under.
type Record interface {
	Version() int
}

// Store appends records to a JSONL file. It holds no open file handle: each
// hook invocation is a separate short-lived process that appends once.
type Store[T Record] struct {
	path string
}

// OpenEvents prepares the behavioral event log for appending.
func OpenEvents(dir string) (*Store[event.Event], error) { return open[event.Event](dir, EventsFile) }

// OpenUsage prepares the usage log for appending.
func OpenUsage(dir string) (*Store[event.Usage], error) { return open[event.Usage](dir, UsageFile) }

func open[T Record](dir, name string) (*Store[T], error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return &Store[T]{path: filepath.Join(dir, name)}, nil
}

// Path reports the log location.
func (s *Store[T]) Path() string { return s.path }

// Append writes one record as a single line.
//
// The record is serialized in full before the file is touched, so a record that
// cannot be encoded writes nothing and leaves earlier records untouched.
//
// Parallel hook processes append to this file concurrently. Interleaving is
// avoided because a record under MaxRecordBytes reaches the kernel as one
// write, which local filesystems serialize against other writers. Two caveats
// come with that. Go retries a short write as a second syscall, so a record can
// still be torn if the filesystem fills up mid-write. And network filesystems,
// NFS in particular, emulate O_APPEND by writing at a computed offset and can
// interleave or lose records outright, so the data directory should stay on
// local storage.
func (s *Store[T]) Append(rec T) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	if buf.Len() > MaxRecordBytes {
		return fmt.Errorf("record of %d bytes exceeds the %d byte limit", buf.Len(), MaxRecordBytes)
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	_, writeErr := f.Write(buf.Bytes())
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("append record: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close log: %w", closeErr)
	}
	return nil
}
