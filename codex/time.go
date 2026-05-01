package codex

import (
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/schema"
)

// Time returns a Codec for time.Time using RFC 3339 (ISO 8601) encoding.
// Values are normalized to UTC on encode. Schema format is "date-time".
func Time() Codec[time.Time] {
	return Codec[time.Time]{
		Schema: schema.Schema{Type: "string", Format: "date-time"},
		Encode: func(v time.Time) (any, error) {
			return v.UTC().Format(time.RFC3339), nil
		},
		Decode: func(v any) (time.Time, error) {
			s, ok := v.(string)
			if !ok {
				return time.Time{}, fmt.Errorf("expected string, got %T", v)
			}
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return time.Time{}, fmt.Errorf("invalid RFC 3339 timestamp %q: %w", s, err)
			}
			return t, nil
		},
	}
}

// Date returns a Codec for time.Time using date-only encoding (2006-01-02).
// The time component is ignored on encode. Decoded values have time set to midnight UTC.
// Schema format is "date".
func Date() Codec[time.Time] {
	const layout = "2006-01-02"
	return Codec[time.Time]{
		Schema: schema.Schema{Type: "string", Format: "date"},
		Encode: func(v time.Time) (any, error) {
			return v.UTC().Format(layout), nil
		},
		Decode: func(v any) (time.Time, error) {
			s, ok := v.(string)
			if !ok {
				return time.Time{}, fmt.Errorf("expected string, got %T", v)
			}
			t, err := time.Parse(layout, s)
			if err != nil {
				return time.Time{}, fmt.Errorf("invalid date %q: %w", s, err)
			}
			return t, nil
		},
	}
}
