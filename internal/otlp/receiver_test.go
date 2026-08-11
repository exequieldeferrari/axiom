package otlp_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/exequieldeferrari/axiom/internal/otlp"
)

// sink collects what a receiver accepts.
type sink struct {
	mu      sync.Mutex
	records []otlp.Record
}

func (s *sink) add(recs []otlp.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, recs...)
}

func (s *sink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func post(t *testing.T, rc *otlp.Receiver, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	w := httptest.NewRecorder()
	rc.ServeHTTP(w, req)
	return w
}

func export(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, otlp.LogsPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestReceiverAcceptsARealExport(t *testing.T) {
	t.Parallel()

	var got sink
	rc := otlp.NewReceiver(got.add)
	w := post(t, rc, export(fixture(t)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// An exporter retries anything it cannot read as a success, which would
	// duplicate the batch.
	if body := w.Body.String(); body != "{}" {
		t.Errorf("body = %q, want an empty ExportLogsServiceResponse", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	if got.len() != 4 {
		t.Errorf("sink received %d records, want 4", got.len())
	}
	if rc.Rejected() != 0 {
		t.Errorf("Rejected = %d, want 0", rc.Rejected())
	}
}

func TestReceiverAcceptsGzip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(fixture(t)); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var got sink
	req := export(buf.Bytes())
	req.Header.Set("Content-Encoding", "gzip")
	w := post(t, otlp.NewReceiver(got.add), req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	if got.len() != 4 {
		t.Errorf("sink received %d records, want 4", got.len())
	}
}

// compress returns body as a gzip request, and how large it is on the wire.
func compress(t *testing.T, body []byte) (*http.Request, int) {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	req := export(buf.Bytes())
	req.Header.Set("Content-Encoding", "gzip")
	return req, buf.Len()
}

// A compressed export can be small on the wire and enormous once expanded.
// Stopping the read at the limit would hand the decoder a truncated document
// and answer "your JSON is broken" to an export that was simply too big.
func TestGzipRejectsAnOversizedPayload(t *testing.T) {
	t.Parallel()

	req, wire := compress(t, bytes.Repeat([]byte("a"), otlp.MaxBodyBytes+1))
	if wire >= otlp.MaxBodyBytes {
		t.Fatalf("the compressed body is %d bytes, which the transport limit would catch on its own", wire)
	}

	var got sink
	rc := otlp.NewReceiver(got.add)
	w := post(t, rc, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413: %s", w.Code, w.Body)
	}
	if got.len() != 0 {
		t.Errorf("sink received %d records from a rejected request", got.len())
	}
	if rc.Rejected() != 1 {
		t.Errorf("Rejected = %d, want 1", rc.Rejected())
	}
}

// The byte that separates "exactly the limit" from "over it" is the one the
// old boundary could not see.
func TestGzipAcceptsAPayloadExactlyAtTheLimit(t *testing.T) {
	t.Parallel()

	// Valid JSON padded with spaces to exactly the limit.
	payload := []byte(`{"resourceLogs":[]}`)
	payload = append(payload, bytes.Repeat([]byte(" "), otlp.MaxBodyBytes-len(payload))...)
	if len(payload) != otlp.MaxBodyBytes {
		t.Fatalf("payload is %d bytes, want exactly %d", len(payload), otlp.MaxBodyBytes)
	}

	req, _ := compress(t, payload)
	if w := post(t, otlp.NewReceiver(func([]otlp.Record) {}), req); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", w.Code, w.Body)
	}
}

// A compressed body can also be oversized on the wire, in which case the
// transport limit is reached inside the gzip reader and has to survive being
// passed back through it.
func TestGzipRejectsAnOversizedTransport(t *testing.T) {
	t.Parallel()

	// Incompressible, so the request is larger than the limit before it is
	// ever expanded.
	noise := make([]byte, otlp.MaxBodyBytes+(1<<20))
	if _, err := rand.New(rand.NewSource(1)).Read(noise); err != nil {
		t.Fatalf("generate noise: %v", err)
	}
	req, wire := compress(t, noise)
	if wire <= otlp.MaxBodyBytes {
		t.Fatalf("the compressed body is %d bytes, which does not exceed the transport limit", wire)
	}

	var got sink
	w := post(t, otlp.NewReceiver(got.add), req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413: %s", w.Code, w.Body)
	}
	if got.len() != 0 {
		t.Errorf("sink received %d records from a rejected request", got.len())
	}
}

// An unreadable export and an oversized one are different problems, and only
// one of them is worth resending in smaller batches.
func TestMalformedGzipIsABadRequest(t *testing.T) {
	t.Parallel()

	valid, _ := compress(t, fixture(t))
	truncated, err := io.ReadAll(valid.Body)
	if err != nil {
		t.Fatalf("read compressed body: %v", err)
	}

	cases := map[string][]byte{
		"not gzip at all":    []byte("{\"resourceLogs\":[]}"),
		"a corrupt header":   {0x1f, 0x8b, 0x08, 0xff, 0xff, 0xff},
		"a truncated stream": truncated[:len(truncated)/2],
		"a corrupt trailer":  append(append([]byte(nil), truncated[:len(truncated)-4]...), 0, 0, 0, 0),
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := export(body)
			req.Header.Set("Content-Encoding", "gzip")

			var got sink
			rc := otlp.NewReceiver(got.add)
			w := post(t, rc, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", w.Code, w.Body)
			}
			if got.len() != 0 {
				t.Errorf("sink received %d records from a rejected request", got.len())
			}
			if rc.Rejected() != 1 {
				t.Errorf("Rejected = %d, want 1", rc.Rejected())
			}
		})
	}
}

func TestReceiverRejectsWhatItCannotIngest(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		req  func() *http.Request
		want int
	}{
		"another path": {
			req:  func() *http.Request { return httptest.NewRequest(http.MethodPost, "/v1/metrics", nil) },
			want: http.StatusNotFound,
		},
		"a GET": {
			req:  func() *http.Request { return httptest.NewRequest(http.MethodGet, otlp.LogsPath, nil) },
			want: http.StatusMethodNotAllowed,
		},
		"protobuf": {
			req: func() *http.Request {
				r := export(nil)
				r.Header.Set("Content-Type", "application/x-protobuf")
				return r
			},
			want: http.StatusUnsupportedMediaType,
		},
		"malformed JSON": {
			req:  func() *http.Request { return export([]byte(`{"resourceLogs":`)) },
			want: http.StatusBadRequest,
		},
		"an oversized body": {
			req:  func() *http.Request { return export(bytes.Repeat([]byte("a"), otlp.MaxBodyBytes+1)) },
			want: http.StatusRequestEntityTooLarge,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got sink
			rc := otlp.NewReceiver(got.add)
			w := post(t, rc, tc.req())

			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body)
			}
			if got.len() != 0 {
				t.Errorf("sink received %d records from a rejected request", got.len())
			}
			// A rejection nobody counts looks exactly like an idle agent.
			if rc.Rejected() != 1 {
				t.Errorf("Rejected = %d, want 1", rc.Rejected())
			}
		})
	}
}

// The wrong protocol is the likeliest misconfiguration, so the response has to
// name the variable that fixes it.
func TestProtocolMismatchExplainsItself(t *testing.T) {
	t.Parallel()

	req := export(nil)
	req.Header.Set("Content-Type", "application/x-protobuf")
	body := post(t, otlp.NewReceiver(func([]otlp.Record) {}), req).Body.String()

	if !strings.Contains(body, "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/json") {
		t.Errorf("body = %q, want the fix", body)
	}
}

func TestReceiverAcceptsACharsetParameter(t *testing.T) {
	t.Parallel()

	req := export(fixture(t))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if w := post(t, otlp.NewReceiver(func([]otlp.Record) {}), req); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", w.Code, w.Body)
	}
}

// An export carrying nothing Axiom reads is still a successful export.
func TestEmptyExportIsAccepted(t *testing.T) {
	t.Parallel()

	called := false
	rc := otlp.NewReceiver(func([]otlp.Record) { called = true })
	w := post(t, rc, export([]byte(`{"resourceLogs":[]}`)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if called {
		t.Error("the sink was called with no records")
	}
	if rc.Rejected() != 0 {
		t.Errorf("Rejected = %d, want 0", rc.Rejected())
	}
}

func TestReceiverServesOverHTTP(t *testing.T) {
	t.Parallel()

	var got sink
	srv := httptest.NewServer(otlp.NewReceiver(got.add))
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+otlp.LogsPath, "application/json", bytes.NewReader(fixture(t)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK || string(body) != "{}" {
		t.Errorf("status = %d, body = %q", resp.StatusCode, body)
	}
	if got.len() != 4 {
		t.Errorf("sink received %d records, want 4", got.len())
	}
}
