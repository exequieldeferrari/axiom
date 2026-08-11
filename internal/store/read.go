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
	// Malformed counts complete lines that are not a decodable record.
	Malformed int
	// Truncated counts a final record with no terminating newline, which is
	// what a crash or a full disk leaves behind.
	Truncated int
	// UnknownVersion counts records written under a schema Axiom does not know.
	UnknownVersion int
}

// Skipped reports how many records the scan could not use.
func (s ScanStats) Skipped() int { return s.Malformed + s.Truncated + s.UnknownVersion }

// Scanner streams records from an append-only JSONL log.
//
// Records are decoded one at a time so that a log larger than memory can still
// be analyzed, and a record Axiom cannot use is counted rather than fatal: the
// writer is fail-open, so the reader has to expect gaps.
type Scanner[T Record] struct {
	buf   *bufio.Reader
	file  *os.File
	rec   T
	stats ScanStats
	err   error
	done  bool
}

// ScanEvents opens the behavioral event log in dir for reading. The log is
// never created or modified; a missing log is reported as an error wrapping
// fs.ErrNotExist.
func ScanEvents(dir string) (*Scanner[event.Event], error) {
	return scan[event.Event](dir, EventsFile)
}

// ScanUsage opens the usage log in dir for reading, under the same terms as
// ScanEvents.
func ScanUsage(dir string) (*Scanner[event.Usage], error) {
	return scan[event.Usage](dir, UsageFile)
}

func scan[T Record](dir, name string) (*Scanner[T], error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	s := NewScanner[T](f)
	s.file = f
	return s, nil
}

// NewScanner streams records from r.
func NewScanner[T Record](r io.Reader) *Scanner[T] {
	// Sizing the buffer to the writer's record limit turns an over-long line
	// into a reportable error instead of an unbounded allocation.
	return &Scanner[T]{buf: bufio.NewReaderSize(r, MaxRecordBytes)}
}

// Close releases the log file when the scanner opened one.
func (s *Scanner[T]) Close() error {
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

// Scan advances to the next usable record, reporting false at the end of the
// log or on an I/O error. Unusable records are counted in Stats and skipped.
func (s *Scanner[T]) Scan() bool {
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
			s.err = fmt.Errorf("read log: %w", err)
		}
	}
	return false
}

// Record returns the record found by the last call to Scan.
func (s *Scanner[T]) Record() T { return s.rec }

// Stats reports what the scan could not use so far.
func (s *Scanner[T]) Stats() ScanStats { return s.stats }

// Err reports an I/O failure. Unusable records are not errors.
func (s *Scanner[T]) Err() error { return s.err }

func (s *Scanner[T]) decode(line []byte) bool {
	var rec T
	if json.Unmarshal(line, &rec) != nil {
		s.stats.Malformed++
		return false
	}
	// A newer schema may give the same field a different meaning, so analyzing
	// it under today's assumptions would be a guess.
	if rec.Version() != event.SchemaVersion {
		s.stats.UnknownVersion++
		return false
	}
	s.rec = rec
	return true
}

// discardLine skips past the remainder of an over-long record.
func (s *Scanner[T]) discardLine() {
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
			s.err = fmt.Errorf("read log: %w", err)
			return
		}
	}
}
