package templ

import (
	"bytes"
	"context"
	"fmt"
	"io"

	atempl "github.com/a-h/templ"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// DecodeNotSupportedError is returned by the templ format's Unmarshal function.
// HTML responses cannot be decoded back into a typed value.
// Use [errors.As] to detect this error when inspecting format decode failures.
type DecodeNotSupportedError struct {
	// ContentType is the content type of the response that cannot be decoded.
	ContentType string
}

func (e DecodeNotSupportedError) Error() string {
	return fmt.Sprintf("templ: %q response cannot be decoded as a typed value", e.ContentType)
}

// Format wraps a templ component as a [format.Format] with ContentType
// "text/html; charset=utf-8". Add it to a route's ResponseFormats to enable
// HTML rendering via the existing [adapters/nethttp] or [adapters/chi] adapters.
//
// Props are validated via c (all Refine constraints run) before the component
// renders. If validation fails, the adapter returns HTTP 500 before any HTML
// is written.
//
// The component is rendered with [context.Background]. Pass all data the
// component needs through the Props struct.
//
// The response is fully buffered — the rendered HTML is held in memory before
// the response is written. Use [StreamingFormat] to render directly to the
// response writer without buffering.
func Format[Props any](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props] {
	const ct = "text/html; charset=utf-8"
	return format.NewTyped(
		c,
		func(props Props) ([]byte, error) {
			var buf bytes.Buffer
			if err := component(props).Render(context.Background(), &buf); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		},
		func([]byte) (Props, error) {
			var zero Props
			return zero, DecodeNotSupportedError{ContentType: ct}
		},
		ct,
	)
}

// StreamingFormat wraps a templ component as a streaming [format.Format] with
// ContentType "text/html; charset=utf-8". Unlike [Format], the component
// renders directly to the [http.ResponseWriter] without buffering to a
// []byte intermediate — ideal for large HTML pages.
//
// Props are validated via c (all Refine constraints run) before the component
// renders; validation errors are caught before response headers are committed.
// If rendering fails after headers are written, the client receives a truncated
// response.
//
// The component is rendered with [context.Background]. Pass all data the
// component needs through the Props struct.
func StreamingFormat[Props any](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props] {
	const ct = "text/html; charset=utf-8"
	return format.NewStreamed(
		c,
		func(props Props, w io.Writer) error {
			return component(props).Render(context.Background(), w)
		},
		func([]byte) (Props, error) {
			var zero Props
			return zero, DecodeNotSupportedError{ContentType: ct}
		},
		ct,
	)
}
