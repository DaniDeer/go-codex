package stream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	stream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared codec for source tests ─────────────────────────────────────────────

type reading struct {
	Sensor string
	Value  float64
}

var readingCodec = codex.Struct(
	codex.RequiredField("sensor",
		codex.String().Refine(validate.NonEmptyString),
		func(r reading) string { return r.Sensor },
		func(r *reading, v string) { r.Sensor = v }),
	codex.RequiredField("value",
		codex.Float64(),
		func(r reading) float64 { return r.Value },
		func(r *reading, v float64) { r.Value = v }),
)

// ── From ──────────────────────────────────────────────────────────────────────

func TestFrom_HappyPath(t *testing.T) {
	ctx := context.Background()
	src := make(chan int, 3)
	src <- 1
	src <- 2
	src <- 3
	close(src)

	s := stream.From(ctx, src)
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 3 {
		t.Errorf("want 3 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

func TestFrom_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := make(chan int) // never closed
	s := stream.From(ctx, src)
	cancel()
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 0 || len(errs) != 0 {
		t.Errorf("want empty after cancel, got %d vals %d errs", len(vals), len(errs))
	}
}

// ── FromCodec ─────────────────────────────────────────────────────────────────

func TestFromCodec_HappyPath(t *testing.T) {
	ctx := context.Background()
	src := make(chan []byte, 2)
	r1, _ := json.Marshal(map[string]any{"sensor": "s1", "value": 42.0})
	r2, _ := json.Marshal(map[string]any{"sensor": "s2", "value": 99.0})
	src <- r1
	src <- r2
	close(src)

	s := stream.FromCodec(ctx, src, format.JSON(readingCodec), stream.SourceOptions{Name: "test"})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if vals[0].Sensor != "s1" || vals[1].Sensor != "s2" {
		t.Errorf("unexpected values: %+v", vals)
	}
}

func TestFromCodec_DecodeFailure(t *testing.T) {
	ctx := context.Background()
	src := make(chan []byte, 3)
	good, _ := json.Marshal(map[string]any{"sensor": "s1", "value": 1.0})
	src <- []byte("not json")
	src <- good
	src <- []byte("{}")
	close(src)

	s := stream.FromCodec(ctx, src, format.JSON(readingCodec), stream.SourceOptions{Name: "mqtt/test"})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 1 {
		t.Errorf("want 1 value (good), got %d", len(vals))
	}
	if len(errs) != 2 {
		t.Errorf("want 2 errors (bad payloads), got %d", len(errs))
	}
	for _, e := range errs {
		var sde stream.StreamDecodeError
		if !isStreamDecodeError(e, &sde) {
			t.Errorf("expected StreamDecodeError, got %T: %v", e, e)
		}
		if sde.Source != "mqtt/test" {
			t.Errorf("Source: want %q, got %q", "mqtt/test", sde.Source)
		}
	}
}

func TestFromCodec_ValidationFailure(t *testing.T) {
	// Empty sensor name fails validate.NonEmptyString
	ctx := context.Background()
	src := make(chan []byte, 1)
	bad, _ := json.Marshal(map[string]any{"sensor": "", "value": 1.0})
	src <- bad
	close(src)

	s := stream.FromCodec(ctx, src, format.JSON(readingCodec), stream.SourceOptions{Name: "src"})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 0 {
		t.Errorf("want 0 values, got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error, got %d", len(errs))
	}
}

func TestFromCodec_ObserverReceivesValidationErrors(t *testing.T) {
	ctx := context.Background()
	src := make(chan []byte, 1)
	bad, _ := json.Marshal(map[string]any{"sensor": "", "value": 1.0})
	src <- bad
	close(src)

	spy := &recordingObserver{}
	s := stream.FromCodec(ctx, src, format.JSON(readingCodec), stream.SourceOptions{Observer: spy})
	stream.Collect(ctx, s)
	if len(spy.valErrors) == 0 {
		t.Error("observer should receive RecordValidationError for field failure")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isStreamDecodeError(e error, out *stream.StreamDecodeError) bool {
	if sde, ok := e.(stream.StreamDecodeError); ok {
		*out = sde
		return true
	}
	return false
}

// ── Example functions ─────────────────────────────────────────────────────────

func ExampleFrom() {
	ctx := context.Background()
	ch := make(chan int, 3)
	ch <- 10
	ch <- 20
	ch <- 30
	close(ch)

	s := stream.From(ctx, ch)
	vals, _ := stream.Collect(ctx, s)
	for _, v := range vals {
		fmt.Println(v)
	}
	// Output:
	// 10
	// 20
	// 30
}

func ExampleFromCodec() {
	ctx := context.Background()
	rawCh := make(chan []byte, 2)
	rawCh <- []byte(`{"sensor":"s1","value":23.5}`)
	rawCh <- []byte(`{"sensor":"s2","value":87.3}`)
	close(rawCh)

	s := stream.FromCodec(ctx, rawCh, format.JSON(readingCodec),
		stream.SourceOptions{Name: "example"})
	vals, errs := stream.Collect(ctx, s)
	fmt.Println(len(vals), "values,", len(errs), "errors")
	// Output:
	// 2 values, 0 errors
}
