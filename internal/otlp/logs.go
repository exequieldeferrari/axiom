// Package otlp decodes OTLP log payloads. It knows the wire format and nothing
// about any particular agent: turning a record into something meaningful is the
// job of that agent's adapter.
package otlp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Record is one decoded log record.
//
// Attributes are kept as strings because that is what every consumer needs:
// OTLP allows the same field to arrive as a JSON number in one record and a
// JSON string in another, and normalizing once here keeps that quirk out of
// every caller.
type Record struct {
	// Name is the record's event name, without any vendor prefix.
	Name string
	// Time is when the agent recorded the event.
	Time time.Time
	// Attrs holds the record's attributes.
	Attrs Attrs
}

// Attrs are a log record's attributes.
type Attrs map[string]string

// String reports the value of key, empty when absent.
func (a Attrs) String(key string) string { return a[key] }

// Int reports the value of key as an integer. The second result is false when
// the key is absent or is not a number, which callers use to distinguish a
// measurement of zero from one the agent never reported.
func (a Attrs) Int(key string) (int64, bool) {
	v, ok := a[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		// A float-valued attribute still carries an integer number of units
		// when the fraction is zero, and rejecting it would silently drop it.
		f, ferr := strconv.ParseFloat(v, 64)
		if ferr != nil {
			return 0, false
		}
		return int64(f), true
	}
	return n, true
}

// DecodeLogs reads an OTLP/JSON ExportLogsServiceRequest.
//
// Records are returned flattened: the resource and scope an agent groups its
// records under carry no information Axiom keeps, and resource attributes are
// deliberately dropped rather than merged, because they are where identity and
// machine detail live.
func DecodeLogs(data []byte) ([]Record, error) {
	var req exportLogsRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("decode OTLP logs: %w", err)
	}

	var out []Record
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				out = append(out, lr.record())
			}
		}
	}
	return out, nil
}

type exportLogsRequest struct {
	ResourceLogs []struct {
		ScopeLogs []struct {
			LogRecords []logRecord `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type logRecord struct {
	TimeUnixNano jsonInt     `json:"timeUnixNano"`
	Body         anyValue    `json:"body"`
	Attributes   []attribute `json:"attributes"`
}

type attribute struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

func (r logRecord) record() Record {
	rec := Record{Attrs: make(Attrs, len(r.Attributes))}
	for _, a := range r.Attributes {
		if s, ok := a.Value.text(); ok {
			rec.Attrs[a.Key] = s
		}
	}

	// The name appears both as an attribute and, vendor-prefixed, in the
	// record body. The attribute is preferred because it is the unprefixed
	// form; the body is the fallback for a producer that sets only that.
	rec.Name = rec.Attrs["event.name"]
	if rec.Name == "" {
		rec.Name, _ = r.Body.text()
	}
	if r.TimeUnixNano != 0 {
		rec.Time = time.Unix(0, int64(r.TimeUnixNano)).UTC()
	}
	return rec
}

// anyValue is an OTLP AnyValue. Only the scalar forms are decoded: an agent
// that sends an array or a map is sending something structured, which is
// exactly the kind of payload Axiom does not store.
type anyValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *jsonInt `json:"intValue"`
	DoubleValue *float64 `json:"doubleValue"`
	BoolValue   *bool    `json:"boolValue"`
}

func (v anyValue) text() (string, bool) {
	switch {
	case v.StringValue != nil:
		return *v.StringValue, true
	case v.IntValue != nil:
		return strconv.FormatInt(int64(*v.IntValue), 10), true
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'f', -1, 64), true
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue), true
	default:
		return "", false
	}
}

// jsonInt is a 64-bit integer that accepts both JSON forms. The OTLP JSON
// mapping specifies a quoted string, and Claude Code sends bare numbers for
// some fields and quoted strings for others, sometimes for the same field on
// different records.
type jsonInt int64

func (n *jsonInt) UnmarshalJSON(data []byte) error {
	if len(data) > 1 && data[0] == '"' {
		data = data[1 : len(data)-1]
	}
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	v, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("not an integer: %s", data)
	}
	*n = jsonInt(v)
	return nil
}
