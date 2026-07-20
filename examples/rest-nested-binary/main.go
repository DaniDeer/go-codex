// Package rest-nested-binary demonstrates two extensions to the merge-field
// "one struct, one call" pattern that are easy to assume DON'T work without
// checking:
//
//  1. Non-JSON body formats — Gob here — compose with the merge-field
//     convenience exactly like JSON/YAML/TOML, because body decode/encode
//     is completely orthogonal to var-merge (codex.DecodeVars/EncodeVars
//     only ever touch a map[string]string, never body bytes).
//  2. Nested struct composition — Req/Resp built from sub-structs (Meta for
//     header/query fields, Payload for the body) instead of flat top-level
//     fields — works out of the box, because merge-field get/set are plain
//     Go closures, not reflection over T's direct fields.
//
// A subtlety worth calling out explicitly: format.Gob(codec) — the
// convenience constructor — serialises the WHOLE typed value via
// encoding/gob's own reflection, bypassing the codec's Encode/Decode
// entirely for the wire bytes (the codec is only used for Validate). That
// means format.Gob(reqCodec) on a NESTED UploadReq would gob-encode ID and
// Meta too, not just Payload — harmless (DecodeMerged always merges
// path/header/query AFTER body decode, so the authoritative HTTP values
// win regardless), but wasteful. When you want the wire bytes to represent
// ONLY the nested Payload sub-field, use format.NewTyped with a custom
// marshal/unmarshal that projects onto/from that sub-field manually — that
// is exactly what this example does.
//
// Run with: go run ./examples/rest-nested-binary
package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// maxUploadBytes caps the accepted payload size.
const maxUploadBytes = 5 * 1024 * 1024

// ── Domain types (nested composition) ──────────────────────────────────────

// UploadPayload is the actual binary content — the ONLY part that travels
// as Gob-encoded HTTP body bytes (see uploadGobFormat below).
type UploadPayload struct {
	Filename string
	Data     []byte
}

// UploadMeta holds header/query-derived fields — populated purely via
// merge fields from the HTTP header/query string, never part of the body
// wire bytes.
type UploadMeta struct {
	ContentHash string // header: X-Content-Hash
	Compress    string // query: compress
}

// UploadReq is the top-level, NESTED request struct: ID from the path,
// Meta from header+query merge fields (nested closures), Payload from the
// Gob body (projected via format.NewTyped, not format.Gob directly).
type UploadReq struct {
	ID      string
	Meta    UploadMeta
	Payload UploadPayload
}

// RespMeta holds response-header-derived fields.
type RespMeta struct {
	TraceID string // response header: X-Trace-Id
}

// UploadResp is the nested response struct: Status/Size are the JSON body,
// Meta.TraceID is a response header merge field (nested closure).
type UploadResp struct {
	Status string
	Size   int
	Meta   RespMeta
}

// ── Codecs ──────────────────────────────────────────────────────────────────

// uploadPayloadCodec validates the Gob-decoded payload's constraints
// (non-empty filename, size limit) — used by uploadGobFormat's unmarshal
// step, mirroring how format.Gob itself calls Codec.Validate post-decode.
var uploadPayloadCodec = codex.Struct[UploadPayload](
	codex.RequiredField("filename", codex.String().Refine(validate.NonEmptyString),
		func(p UploadPayload) string { return p.Filename },
		func(p *UploadPayload, v string) { p.Filename = v },
	),
	codex.RequiredField("data", codex.Bytes().Refine(validate.MaxBytes(maxUploadBytes)),
		func(p UploadPayload) []byte { return p.Data },
		func(p *UploadPayload, v []byte) { p.Data = v },
	),
)

// reqCodec is a placeholder Codec[UploadReq] with no declared fields — the
// ACTUAL wire bytes are produced by uploadGobFormat below, not by this
// codec's own (unused) Encode/Decode. ID and Meta are populated exclusively
// via merge fields (path/header/query), never via a body codec field.
var reqCodec = codex.Struct[UploadReq]()

// uploadGobFormat projects the Gob wire bytes onto JUST req.Payload.
//
// format.Gob(reqCodec) would instead gob-encode EVERY exported field of
// UploadReq (ID, Meta, Payload) — Gob serialises the typed value directly
// via reflection, bypassing the codec's Encode/Decode entirely. Use
// format.NewTyped with a custom marshal/unmarshal whenever a whole-value
// wire format (Gob, protobuf, a custom binary layout) should represent only
// a NESTED sub-field of a larger Req.
var uploadGobFormat = format.NewTyped[UploadReq](
	reqCodec,
	func(r UploadReq) ([]byte, error) {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(r.Payload); err != nil {
			return nil, fmt.Errorf("gob encode payload: %w", err)
		}
		return buf.Bytes(), nil
	},
	func(data []byte) (UploadReq, error) {
		var p UploadPayload
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&p); err != nil {
			return UploadReq{}, fmt.Errorf("gob decode payload: %w", err)
		}
		if err := uploadPayloadCodec.Validate(p); err != nil {
			return UploadReq{}, err
		}
		return UploadReq{Payload: p}, nil
	},
	"application/gob",
)

// respCodec declares ONLY the JSON body fields (status, size) — Meta.TraceID
// is deliberately NOT declared here; it is a response header merge field
// instead (see rest.NewRequiredResponseHeaderParam below), populated by the
// adapter automatically after the handler returns.
var respCodec = codex.Struct[UploadResp](
	codex.RequiredField("status", codex.String(),
		func(r UploadResp) string { return r.Status },
		func(r *UploadResp, v string) { r.Status = v },
	),
	codex.RequiredField("size", codex.Int(),
		func(r UploadResp) int { return r.Size },
		func(r *UploadResp, v int) { r.Size = v },
	),
)

// uploadRoute is the shared contract — server and client both build their
// handle from this SAME Route value, then each registers uploadGobFormat as
// the accepted/produced request format.
var uploadRoute = rest.NewRoute[UploadReq, UploadResp]("POST", "/uploads/{id}",
	reqCodec, respCodec,
	rest.RouteMeta{
		OperationID: "uploadFile",
		Summary:     "Upload a file — Gob body projected onto a nested Payload field, header/query merged into a nested Meta field",
	},
	// Path: nested closure would be unnecessary here (ID is top-level), but
	// header/query below demonstrate the nested case (Meta.X).
	rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
		func(r UploadReq) string { return r.ID },
		func(r *UploadReq, v string) { r.ID = v },
	).WithDescription("Upload resource ID"),
	// Header merge field targeting a NESTED struct field — get/set are
	// plain closures, so req.Meta.ContentHash is exactly as easy to reach
	// as a top-level field would be. No framework change needed for this.
	rest.NewOptionalHeaderParam("X-Content-Hash", codex.String(),
		func(r UploadReq) string { return r.Meta.ContentHash },
		func(r *UploadReq, v string) { r.Meta.ContentHash = v },
	).WithDescription("Client-supplied content hash for integrity checking"),
	// Query merge field, same nested pattern.
	rest.NewOptionalQueryParam("compress", codex.String(),
		func(r UploadReq) string { return r.Meta.Compress },
		func(r *UploadReq, v string) { r.Meta.Compress = v },
	).WithDescription("Requested compression algorithm"),
	// Response header merge field targeting a NESTED response struct field.
	rest.NewRequiredResponseHeaderParam("X-Trace-Id", codex.String().Refine(validate.NonEmptyString),
		func(r UploadResp) string { return r.Meta.TraceID },
		func(r *UploadResp, v string) { r.Meta.TraceID = v },
	).WithDescription("Server-generated tracing ID for this upload"),
)

func main() {
	// ── Server ───────────────────────────────────────────────────────────────

	b := rest.NewBuilder(rest.Info{Title: "Nested Binary Upload API", Version: "1.0.0"})
	serverHandle, err := uploadRoute.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		os.Exit(1)
	}
	// Accept Gob request bodies (application/gob) instead of the JSON default.
	serverHandle.WithRequestFormats(uploadGobFormat)

	mux := http.NewServeMux()
	nethttp.Register(mux, serverHandle, func(_ context.Context, req UploadReq) (UploadResp, error) {
		// req arrives FULLY merged: ID (path), Meta.ContentHash (header),
		// Meta.Compress (query), Payload (Gob body) — one struct, no manual
		// r.PathValue()/r.URL.Query()/r.Header.Get() calls needed.
		fmt.Printf("server received: id=%q meta=%+v payload.filename=%q payload.size=%d\n",
			req.ID, req.Meta, req.Payload.Filename, len(req.Payload.Data))

		return UploadResp{
			Status: "stored",
			Size:   len(req.Payload.Data),
			Meta:   RespMeta{TraceID: "trace-" + req.ID},
		}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── Client ───────────────────────────────────────────────────────────────

	clientHandle := uploadRoute.ClientHandle()
	clientHandle.WithRequestFormats(uploadGobFormat)

	req := UploadReq{
		ID:      "f47ac10b",
		Meta:    UploadMeta{ContentHash: "sha256:abc123", Compress: "gzip"},
		Payload: UploadPayload{Filename: "report.pdf", Data: []byte("binary report contents")},
	}

	fmt.Println("=== One struct, one call: nested Req, Gob body, header+query merge ===")
	resp, err := nethttp.CallHandle(context.Background(), srv.Client(), srv.URL, clientHandle, req, nethttp.CallOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "call:", err)
		os.Exit(1)
	}
	fmt.Printf("client received: %+v\n", resp)
	fmt.Println("(resp.Meta.TraceID was merged automatically from the X-Trace-Id response header)")
}
