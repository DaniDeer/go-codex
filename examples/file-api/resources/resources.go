// Package resources is the shared contract package for examples/file-api
// — domain types, codecs, and [file.Resource] declarations every
// demo_*.go imports (mirrors examples/rest-api/events-api/reqreply-api's
// own routes/ package). Every Resource here uses a RELATIVE path
// template (no directory baked in) — main.go's [file.Router] supplies
// the runtime temp-directory prefix at Handle-build time, so these
// declarations are themselves backend/environment-agnostic: the exact
// same values would work unchanged against a future adapters/s3/
// adapters/smb backend.
package resources

import (
	"fmt"
	"io"
	"strings"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// Note is the domain type stored, one JSON file per note.
type Note struct {
	Title string
	Body  string
}

func NoteCodec() codex.Codec[Note] {
	return codex.Struct[Note](
		codex.RequiredField("title", codex.String(),
			func(n Note) string { return n.Title },
			func(n *Note, v string) { n.Title = v }),
		codex.RequiredField("body", codex.String(),
			func(n Note) string { return n.Body },
			func(n *Note, v string) { n.Body = v }),
	)
}

// Notes is the shared resource every CRUD/List/Copy/Move/Mkdir/
// BatchDelete/StreamRead/StreamWrite demo declares its roles against.
//
// file.PathParam.WithCodec validates every {id} path variable against a
// codex.Codec[string] BEFORE the path is built — mirrors
// rest.PathParam.WithCodec/events.TopicParam.WithCodec exactly. Here,
// every id must be a URL-friendly slug (lowercase alphanumeric +
// hyphens); an invalid id never reaches the backend at all.
var Notes = file.NewResource[Note]("{id}.json", format.JSON(NoteCodec()),
	file.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.Slug)),
)

// Archive is a directory-only resource (no {var} placeholder) for the
// Mkdir demo — "archive" is relative, resolved against the SAME Router
// prefix as Notes.
var Archive = file.NewResource[Note]("archive", format.JSON(NoteCodec()))

// CategorizedNotes demonstrates a 2-path-variable template — the
// Recursive WithList demo's nested category/id directory structure
// (notes/{category}/{id}.json).
var CategorizedNotes = file.NewResource[Note]("notes/{category}/{id}.json", format.JSON(NoteCodec()),
	file.PathParam{Name: "category"},
	file.PathParam{Name: "id"},
)

// BodyPatch is Patch's own, smaller partial-update codec — only Body is
// patchable, Title is never touched by a Patch call.
type BodyPatch struct{ Body *string }

func BodyPatchCodec() codex.Codec[BodyPatch] {
	return codex.PartialStruct[BodyPatch](
		codex.PartialField("body", codex.String(),
			func(p BodyPatch) *string { return p.Body },
			func(p *BodyPatch, v *string) { p.Body = v }),
	)
}

// MetadataNote demonstrates the file-metadata merge-field axis: Owner is
// populated from the Backend's sidecar metadata file, NEVER from the
// JSON codec itself — a MetadataNote's own JSON body never mentions
// "owner" at all.
type MetadataNote struct {
	Title string
	Body  string
	Owner string
}

func MetadataNoteCodec() codex.Codec[MetadataNote] {
	return codex.Struct[MetadataNote](
		codex.RequiredField("title", codex.String(),
			func(n MetadataNote) string { return n.Title },
			func(n *MetadataNote, v string) { n.Title = v }),
		codex.RequiredField("body", codex.String(),
			func(n MetadataNote) string { return n.Body },
			func(n *MetadataNote, v string) { n.Body = v }),
	)
}

// MetadataNotes declares Owner as a MergedMetadataParam — Read/List
// automatically populate it from the Backend's own sidecar metadata
// file; Create/Patch automatically persist it there.
var MetadataNotes = file.NewResource[MetadataNote]("meta/{id}.json", format.JSON(MetadataNoteCodec()),
	file.PathParam{Name: "id"},
	file.NewMetadataParam("owner", codex.String(),
		func(n MetadataNote) string { return n.Owner },
		func(n *MetadataNote, v string) { n.Owner = v }),
)

// StreamableNote + StreamableNoteFormat demonstrate Mechanism 1
// (automatic format-level write streaming, see docs/design/d-0011-file-api-backend-transparency.md's
// "Streaming/chunked reads and writes" section): a format built via
// format.NewStreamed writes directly to the Backend's own io.Writer
// (StreamWriteBackend.OpenWriter), with ZERO declare-time changes needed
// on the resource/role side — createCore detects IsStreamable()
// automatically.
type StreamableNote struct {
	Title string
	Body  string
}

func StreamableNoteCodec() codex.Codec[StreamableNote] {
	return codex.Struct[StreamableNote](
		codex.RequiredField("title", codex.String(),
			func(n StreamableNote) string { return n.Title },
			func(n *StreamableNote, v string) { n.Title = v }),
		codex.RequiredField("body", codex.String(),
			func(n StreamableNote) string { return n.Body },
			func(n *StreamableNote, v string) { n.Body = v }),
	)
}

// StreamableNoteFormat is a plain-text streaming format: Title and Body
// are written directly to the destination io.Writer, one per line — no
// []byte intermediate ever materializes on the write side.
func StreamableNoteFormat() format.Format[StreamableNote] {
	return format.NewStreamed(StreamableNoteCodec(),
		func(v StreamableNote, w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s\n%s", v.Title, v.Body)
			return err
		},
		func(data []byte) (StreamableNote, error) {
			title, body, _ := strings.Cut(string(data), "\n")
			return StreamableNote{Title: title, Body: body}, nil
		},
		"text/plain; charset=utf-8",
	)
}

// StreamableNotes is the resource used for the Mechanism-1 half of
// demo_streaming_io.go.
var StreamableNotes = file.NewResource[StreamableNote]("streamable/{id}.txt", StreamableNoteFormat(),
	file.PathParam{Name: "id"},
)

// GrantedScopes is a BoundMiddleware Out type — the conventionally-named
// GrantedScopes field (map[string][]string), read via reflection by
// api/file's dispatch core, exactly mirroring api/rest/api/events' own
// Security-attachment convention.
type GrantedScopes struct {
	GrantedScopes map[string][]string
}
