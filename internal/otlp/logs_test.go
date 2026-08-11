package otlp_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/exequieldeferrari/axiom/internal/otlp"
)

// fixture is a real export from Claude Code 2.1.226, with personal and
// machine-specific values replaced. Its JSON types are exactly as captured,
// which is the point: they are not what the OTLP JSON mapping suggests.
func fixture(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile("testdata/claude_logs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func decode(t *testing.T, data []byte) map[string]otlp.Record {
	t.Helper()

	records, err := otlp.DecodeLogs(data)
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	byName := make(map[string]otlp.Record, len(records))
	for _, r := range records {
		byName[r.Name] = r
	}
	return byName
}

func TestDecodeRealExport(t *testing.T) {
	t.Parallel()

	records, err := otlp.DecodeLogs(fixture(t))
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("decoded %d records, want the 4 in the fixture", len(records))
	}

	// Records arrive flattened, in the order the agent sent them.
	var names []string
	for _, r := range records {
		names = append(names, r.Name)
	}
	want := "hook_registered user_prompt api_request tool_result"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

// The name is carried twice: unprefixed in an attribute and vendor-prefixed in
// the body. Reading the body would leak the vendor prefix into the adapter.
func TestNameComesFromTheAttribute(t *testing.T) {
	t.Parallel()

	if got := decode(t, fixture(t))["tool_result"].Name; got != "tool_result" {
		t.Errorf("Name = %q, want the unprefixed attribute value", got)
	}
}

func TestNameFallsBackToTheBody(t *testing.T) {
	t.Parallel()

	const payload = `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
		{"timeUnixNano":"1786407295840000000","body":{"stringValue":"claude_code.tool_result"}}]}]}]}`

	records, err := otlp.DecodeLogs([]byte(payload))
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	if got := records[0].Name; got != "claude_code.tool_result" {
		t.Errorf("Name = %q, want the body value when no attribute carries it", got)
	}
}

func TestDecodeReadsTheTimestamp(t *testing.T) {
	t.Parallel()

	got := decode(t, fixture(t))["api_request"].Time
	want := time.Unix(0, 1786407295795000000).UTC()
	if !got.Equal(want) {
		t.Errorf("Time = %s, want %s", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("Time is in %s, want UTC", got.Location())
	}
}

// The same field arrives as a JSON number on one record and a quoted string on
// another, in the same export. A decoder that trusted either one alone would
// silently lose half the measurements.
func TestIntegersArriveInBothJSONForms(t *testing.T) {
	t.Parallel()

	records := decode(t, fixture(t))

	// api_request sends duration_ms as a bare JSON number.
	if got, ok := records["api_request"].Attrs.Int("duration_ms"); !ok || got != 3002 {
		t.Errorf("api_request duration_ms = %d, %v; want 3002, true", got, ok)
	}
	// tool_result sends the same field as a quoted string.
	if got, ok := records["tool_result"].Attrs.Int("duration_ms"); !ok || got != 2 {
		t.Errorf("tool_result duration_ms = %d, %v; want 2, true", got, ok)
	}
	if got, ok := records["tool_result"].Attrs.Int("tool_result_size_bytes"); !ok || got != 43 {
		t.Errorf("tool_result_size_bytes = %d, %v; want 43, true", got, ok)
	}
}

// A measurement that was never reported has to stay distinguishable from one
// that was reported as zero.
func TestMissingAttributeIsNotZero(t *testing.T) {
	t.Parallel()

	attrs := decode(t, fixture(t))["tool_result"].Attrs
	if _, ok := attrs.Int("input_tokens"); ok {
		t.Error("an absent attribute reported a value")
	}
	if got, ok := attrs.Int("tool_input_size_bytes"); !ok || got != 72 {
		t.Errorf("a present attribute was lost: %d, %v", got, ok)
	}
	if got := attrs.String("nonexistent"); got != "" {
		t.Errorf("String on an absent key = %q", got)
	}
}

func TestNonNumericAttributeIsNotANumber(t *testing.T) {
	t.Parallel()

	attrs := decode(t, fixture(t))["api_request"].Attrs
	if _, ok := attrs.Int("model"); ok {
		t.Error("a model identifier was read as a number")
	}
}

func TestScalarValueTypes(t *testing.T) {
	t.Parallel()

	const payload = `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[
		{"key":"s","value":{"stringValue":"text"}},
		{"key":"i","value":{"intValue":"42"}},
		{"key":"n","value":{"intValue":42}},
		{"key":"d","value":{"doubleValue":0.213915}},
		{"key":"b","value":{"boolValue":true}},
		{"key":"empty","value":{}},
		{"key":"list","value":{"arrayValue":{"values":[{"stringValue":"x"}]}}}
	]}]}]}]}`

	records, err := otlp.DecodeLogs([]byte(payload))
	if err != nil {
		t.Fatalf("DecodeLogs: %v", err)
	}
	attrs := records[0].Attrs

	for key, want := range map[string]string{
		"s": "text", "i": "42", "n": "42", "d": "0.213915", "b": "true",
	} {
		if got := attrs.String(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// Structured and empty values carry nothing Axiom stores.
	for _, key := range []string{"empty", "list"} {
		if _, ok := attrs[key]; ok {
			t.Errorf("%s was kept, want it dropped", key)
		}
	}
	if got, ok := attrs.Int("d"); !ok || got != 0 {
		t.Errorf("a whole-unit double = %d, %v; want 0, true", got, ok)
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"truncated":                 `{"resourceLogs":[{"scopeLogs":`,
		"not json":                  `<html>nope</html>`,
		"wrong type for logRecords": `{"resourceLogs":[{"scopeLogs":[{"logRecords":"x"}]}]}`,
		"wrong type for intValue": `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
			{"attributes":[{"key":"i","value":{"intValue":"not-a-number"}}]}]}]}]}`,
	} {
		if _, err := otlp.DecodeLogs([]byte(payload)); err == nil {
			t.Errorf("%s: decoded without error", name)
		}
	}
}

func TestDecodeAcceptsAnEmptyExport(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{`{}`, `{"resourceLogs":[]}`, `{"resourceLogs":[{"scopeLogs":[]}]}`} {
		records, err := otlp.DecodeLogs([]byte(payload))
		if err != nil {
			t.Errorf("%s: %v", payload, err)
		}
		if len(records) != 0 {
			t.Errorf("%s: decoded %d records, want none", payload, len(records))
		}
	}
}

// Resource attributes are where identity and machine detail live. Flattening
// them into the record would smuggle them past the adapter's allowlist.
func TestResourceAttributesAreNotMerged(t *testing.T) {
	t.Parallel()

	for _, rec := range decode(t, fixture(t)) {
		for _, key := range []string{"service.name", "service.version", "os.type", "os.version", "host.arch"} {
			if _, ok := rec.Attrs[key]; ok {
				t.Errorf("%s: resource attribute %q reached the record", rec.Name, key)
			}
		}
	}
}
