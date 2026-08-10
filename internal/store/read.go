package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/exequieldeferrari/axiom/internal/event"
)

// ScanStats counts records a scan could not use.
//
// Skipped records matter to a profiler: a dropped write could turn a legitimate
// re-read into an apparent redundancy, so callers are expected to surface these
// counts rather than treat a partial read as a complete one.
type ScanStats struct {
	// Malformed counts complete lines that are not a decodable event.
	Malformed int
	// Truncated counts a final record with no terminating newline, which is
	// what a crash or a full disk leaves behind.
	Truncated int
	// UnknownVersion counts events written under a schema Axiom does not know.
	UnknownVersion int
}

// Skipped reports how many records the scan could not use.
func (s ScanStats) Skipped() int { return s.Malformed + s.Truncated + s.UnknownVersion }

// Scanner streams events from an append-only JSONL log.
//
// Records are decoded one at a time so that a log larger than memory can still
// be analyzed, and a record Axiom cannot use is counted rather than fatal: the
// writer is fail-open, so the reader has to expect gaps.
type Scanner struct {
	buf   *bufio.Reader
	file  *os.File
	ev    event.Event
	stats ScanStats
	err   error
	done  bool
}

// Scan opens the event log in dir for reading. The log is never created or
// modified; a missing log is reported as an error wrapping fs.ErrNotExist.
func Scan(dir string) (*Scanner, error) {
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	s := NewScanner(f)
	s.file = f
	return s, nil
}

// NewScanner streams events from r.
func NewScanner(r io.Reader) *Scanner {
	// Sizing the buffer to the writer's record limit turns an over-long line
	// into a reportable error instead of an unbounded allocation.
	return &Scanner{buf: bufio.NewReaderSize(r, MaxRecordBytes)}
}

// Close releases the log file when the scanner opened one.
func (s *Scanner) Close() error {
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

// Scan advances to the next usable event, reporting false at the end of the
// log or on an I/O error. Unusable records are counted in Stats and skipped.
func (s *Scanner) Scan() bool {
	for !s.done && s.err == nil {
		line, err := s.buf.ReadSlice('\n')
		switch {
		case err == nil:
			if s.decode(line) {
				return true
			}
		case errors.Is(err, io.EOF):
			s.done = true
			if len(line) > 0 {
				// The writer emits each record's newline in the same write, so
				// a record without one was never finished.
				s.stats.Truncated++
			}
		case errors.Is(err, bufio.ErrBufferFull):
			// Longer than any record the writer can produce.
			s.stats.Malformed++
			s.discardLine()
		default:
			s.err = fmt.Errorf("read event log: %w", err)
		}
	}
	return false
}

// Event returns the event found by the last call to Scan.
func (s *Scanner) Event() event.Event { return s.ev }

// Stats reports what the scan could not use so far.
func (s *Scanner) Stats() ScanStats { return s.stats }

// Err reports an I/O failure. Unusable records are not errors.
func (s *Scanner) Err() error { return s.err }

func (s *Scanner) decode(line []byte) bool {
	var ev event.Event
	if json.Unmarshal(line, &ev) != nil {
		s.stats.Malformed++
		return false
	}
	// A newer schema may give the same field a different meaning, so analyzing
	// it under today's assumptions would be a guess.
	if ev.SchemaVersion != event.SchemaVersion {
		s.stats.UnknownVersion++
		return false
	}
	s.ev = ev
	return true
}

// discardLine skips past the remainder of an over-long record.
func (s *Scanner) discardLine() {
	for {
		_, err := s.buf.ReadSlice('\n')
		switch {
		case err == nil:
			return
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			s.done = true
			return
		default:
			s.err = fmt.Errorf("read event log: %w", err)
			return
		}
	}
}
