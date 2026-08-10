// Package store persists canonical events to a local append-only JSONL file.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// FileName is the append-only event log inside the data directory.
const FileName = "events.jsonl"

// MaxRecordBytes bounds a single serialized event. Records stay small because
// Axiom stores metadata rather than content, which also keeps every append
// within one write syscall.
const MaxRecordBytes = 64 << 10

// Store appends events to a JSONL file. It holds no open file handle: each
// hook invocation is a separate short-lived process that appends once.
type Store struct {
	path string
}

// Open prepares the data directory for appending.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return &Store{path: filepath.Join(dir, FileName)}, nil
}

// Path reports the event log location.
func (s *Store) Path() string { return s.path }

// Append writes one event as a single line.
//
// The record is serialized in full before the file is touched, so an event that
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
func (s *Store) Append(ev event.Event) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	if buf.Len() > MaxRecordBytes {
		return fmt.Errorf("event of %d bytes exceeds the %d byte limit", buf.Len(), MaxRecordBytes)
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	_, writeErr := f.Write(buf.Bytes())
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("append event: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close event log: %w", closeErr)
	}
	return nil
}
