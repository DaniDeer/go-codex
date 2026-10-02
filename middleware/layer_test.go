package middleware_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type layerIn struct {
	Header string
	Cookie string
	Query  string
}

var layerHeaderField = codex.RequiredField("h", codex.String().Refine(validate.NonEmptyString),
	func(v layerIn) string { return v.Header },
	func(v *layerIn, s string) { v.Header = s })

var layerCookieField = codex.OptionalField("c", codex.String(),
	func(v layerIn) string { return v.Cookie },
	func(v *layerIn, s string) { v.Cookie = s })

var layerQueryField = codex.OptionalField("q", codex.String(),
	func(v layerIn) string { return v.Query },
	func(v *layerIn, s string) { v.Query = s })

var layerOmitField = codex.OmitEmptyField("o", codex.String(),
	func(v layerIn) string { return v.Query },
	func(v *layerIn, s string) { v.Query = s })

type layerWrapError struct{ Err error }

func (e layerWrapError) Error() string { return fmt.Sprintf("layer: %v", e.Err) }
func (e layerWrapError) Unwrap() error { return e.Err }

func wrapLayerErr(err error) error { return layerWrapError{Err: err} }

// ── DecodeLayer ───────────────────────────────────────────────────────────

func TestDecodeLayer_MultiAxisRoundTrip(t *testing.T) {
	axes := []middleware.Axis[layerIn]{
		{Fields: []codex.FieldCodec[layerIn]{layerHeaderField}, Vars: map[string]string{"h": "hv"}},
		{Fields: []codex.FieldCodec[layerIn]{layerCookieField}, Vars: map[string]string{"c": "cv"}},
		{Fields: []codex.FieldCodec[layerIn]{layerQueryField}, Vars: map[string]string{"q": "qv"}},
	}
	got, err := middleware.DecodeLayer(axes, wrapLayerErr)
	if err != nil {
		t.Fatalf("DecodeLayer: %v", err)
	}
	want := layerIn{Header: "hv", Cookie: "cv", Query: "qv"}
	if got != want {
		t.Errorf("want %+v, got %+v", want, got)
	}
}

func TestDecodeLayer_PerAxisErrorWrapping(t *testing.T) {
	axes := []middleware.Axis[layerIn]{
		{Fields: []codex.FieldCodec[layerIn]{layerHeaderField}, Vars: map[string]string{}}, // missing required "h"
	}
	_, err := middleware.DecodeLayer(axes, wrapLayerErr)
	if err == nil {
		t.Fatal("want error for missing required field")
	}
	var wrapped layerWrapError
	if !errors.As(err, &wrapped) {
		t.Fatalf("want wrapErr to have wrapped the error, got %T: %v", err, err)
	}
	var verrs codex.ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("want errors.As to reach codex.ValidationErrors, got %v", err)
	}
}

func TestDecodeLayer_FailFast_LaterAxesNotAttempted(t *testing.T) {
	// axis 1 fails (missing required "h"); axis 2 (cookie) would succeed if
	// attempted — confirm it is NOT, i.e. Cookie stays the Go zero value.
	axes := []middleware.Axis[layerIn]{
		{Fields: []codex.FieldCodec[layerIn]{layerHeaderField}, Vars: map[string]string{}},
		{Fields: []codex.FieldCodec[layerIn]{layerCookieField}, Vars: map[string]string{"c": "should-not-apply"}},
	}
	got, err := middleware.DecodeLayer(axes, wrapLayerErr)
	if err == nil {
		t.Fatal("want error from first failing axis")
	}
	if got.Cookie != "" {
		t.Errorf("want later axis not attempted after first failure, got Cookie=%q", got.Cookie)
	}
}

func TestDecodeLayer_ZeroAxes_ReturnsZeroValueNoError(t *testing.T) {
	got, err := middleware.DecodeLayer([]middleware.Axis[layerIn]{}, wrapLayerErr)
	if err != nil {
		t.Fatalf("want nil error for zero axes, got %v", err)
	}
	var zero layerIn
	if got != zero {
		t.Errorf("want zero value, got %+v", got)
	}
}

func TestDecodeLayer_EmptyFieldsAxis_SkippedAsNoOp(t *testing.T) {
	axes := []middleware.Axis[layerIn]{
		{Fields: nil, Vars: map[string]string{"h": "ignored"}},
		{Fields: []codex.FieldCodec[layerIn]{layerQueryField}, Vars: map[string]string{"q": "qv"}},
	}
	got, err := middleware.DecodeLayer(axes, wrapLayerErr)
	if err != nil {
		t.Fatalf("DecodeLayer: %v", err)
	}
	if got.Query != "qv" || got.Header != "" {
		t.Errorf("want only non-empty-Fields axis applied, got %+v", got)
	}
}

// ── EncodeLayer ───────────────────────────────────────────────────────────

func TestEncodeLayer_MultiAxisRoundTrip(t *testing.T) {
	v := layerIn{Header: "hv", Cookie: "cv", Query: "qv"}
	axisFields := [][]codex.FieldCodec[layerIn]{
		{layerHeaderField},
		{layerCookieField},
		{layerQueryField},
	}
	vars, err := middleware.EncodeLayer(v, axisFields, wrapLayerErr)
	if err != nil {
		t.Fatalf("EncodeLayer: %v", err)
	}
	if len(vars) != 3 {
		t.Fatalf("want 3 axis maps, got %d", len(vars))
	}
	if vars[0]["h"] != "hv" || vars[1]["c"] != "cv" || vars[2]["q"] != "qv" {
		t.Errorf("unexpected vars: %+v", vars)
	}
}

func TestEncodeLayer_SparseOmit(t *testing.T) {
	v := layerIn{Query: ""}
	axisFields := [][]codex.FieldCodec[layerIn]{
		{layerOmitField},
	}
	vars, err := middleware.EncodeLayer(v, axisFields, wrapLayerErr)
	if err != nil {
		t.Fatalf("EncodeLayer: %v", err)
	}
	if _, ok := vars[0]["o"]; ok {
		t.Errorf("want sparse field omitted, got %q", vars[0]["o"])
	}
}

func TestEncodeLayer_EmptyFieldsAxis_ReturnsNilMap(t *testing.T) {
	v := layerIn{Header: "hv"}
	axisFields := [][]codex.FieldCodec[layerIn]{
		{layerHeaderField},
		nil,
	}
	vars, err := middleware.EncodeLayer(v, axisFields, wrapLayerErr)
	if err != nil {
		t.Fatalf("EncodeLayer: %v", err)
	}
	if vars[1] != nil {
		t.Errorf("want nil map for empty-Fields axis, got %v", vars[1])
	}
}

func TestEncodeLayer_PerAxisErrorWrapping(t *testing.T) {
	// A non-string-wire codec attached directly triggers VarEncodeTypeError.
	badField := codex.RequiredField("bad", codex.Int(),
		func(v layerIn) int { return 0 },
		func(v *layerIn, n int) {})
	axisFields := [][]codex.FieldCodec[layerIn]{
		{badField},
	}
	_, err := middleware.EncodeLayer(layerIn{}, axisFields, wrapLayerErr)
	if err == nil {
		t.Fatal("want error for non-string codec")
	}
	var wrapped layerWrapError
	if !errors.As(err, &wrapped) {
		t.Fatalf("want wrapErr to have wrapped the error, got %T: %v", err, err)
	}
}

func TestEncodeLayer_ZeroAxes_ReturnsEmptySliceNoError(t *testing.T) {
	vars, err := middleware.EncodeLayer(layerIn{}, [][]codex.FieldCodec[layerIn]{}, wrapLayerErr)
	if err != nil {
		t.Fatalf("want nil error for zero axes, got %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("want empty slice, got %v", vars)
	}
}
