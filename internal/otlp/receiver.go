package otlp

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// LogsPath is the OTLP endpoint for log records.
const LogsPath = "/v1/logs"

// MaxBodyBytes bounds one export request. Agents batch on a short interval and
// Axiom keeps no content, so a legitimate batch is orders of magnitude smaller.
const MaxBodyBytes = 8 << 20

// Receiver accepts OTLP/JSON log exports over HTTP.
//
// Only the JSON encoding is accepted. Axiom configures the exporter that talks
// to it, so supporting the binary encodings would mean maintaining a protobuf
// decoder for a payload nothing sends.
type Receiver struct {
	sink     func([]Record)
	rejected atomic.Int64
}

// NewReceiver returns a receiver that passes each accepted batch to sink.
//
// sink is called from the server's request goroutines and must be safe to call
// concurrently.
func NewReceiver(sink func([]Record)) *Receiver {
	return &Receiver{sink: sink}
}

// Rejected reports how many requests were refused or could not be decoded.
// Silence about a rejected batch would look exactly like an idle agent.
func (rc *Receiver) Rejected() int64 { return rc.rejected.Load() }

func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != LogsPath {
		rc.reject(w, http.StatusNotFound,
			fmt.Sprintf("axiom receives OTLP logs at %s only", LogsPath))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		rc.reject(w, http.StatusMethodNotAllowed, "OTLP exports are POSTed")
		return
	}
	// A charset or boundary parameter is still JSON.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		rc.reject(w, http.StatusUnsupportedMediaType,
			fmt.Sprintf("axiom accepts application/json, got %q; set OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/json", ct))
		return
	}

	body, err := readBody(r)
	if err != nil {
		rc.reject(w, statusFor(err), err.Error())
		return
	}

	records, err := DecodeLogs(body)
	if err != nil {
		rc.reject(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(records) > 0 {
		rc.sink(records)
	}

	// An empty ExportLogsServiceResponse is a complete success. Exporters
	// retry anything else, so answering precisely keeps batches from being
	// resent and duplicated.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (rc *Receiver) reject(w http.ResponseWriter, code int, msg string) {
	rc.rejected.Add(1)
	http.Error(w, msg, code)
}

// Why an export was refused. The status code is derived from these rather than
// from an error message, and the two answers differ in what they ask of the
// exporter: only an oversized export is worth resending in smaller batches.
var (
	errTooLarge  = fmt.Errorf("export exceeds the %d byte limit", MaxBodyBytes)
	errMalformed = errors.New("export could not be read")
)

func statusFor(err error) int {
	if errors.Is(err, errTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func readBody(r *http.Request) ([]byte, error) {
	// Bounds what is read from the connection, and fails with an error that
	// stays distinguishable, so an oversized transport is not reported as an
	// unreadable one.
	var src io.Reader = http.MaxBytesReader(nil, r.Body, MaxBodyBytes)

	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMalformed, err)
		}
		defer zr.Close()
		src = zr
	}

	// One byte past the limit, so a payload that exactly fills it stays
	// distinguishable from one that overran it. Stopping at the limit instead
	// would hand a truncated body to the decoder, and an export that was too
	// large would be answered as though its JSON were invalid.
	body, err := io.ReadAll(io.LimitReader(src, MaxBodyBytes+1))
	switch {
	case isTooLarge(err):
		return nil, errTooLarge
	case err != nil:
		return nil, fmt.Errorf("%w: %w", errMalformed, err)
	case len(body) > MaxBodyBytes:
		// Only reachable through gzip: the transport reader stops earlier.
		return nil, errTooLarge
	}
	return body, nil
}

// isTooLarge reports whether reading the request body hit the transport limit,
// including when a gzip reader passed that failure through.
func isTooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.As(err, &maxBytes)
}
