// Package pattern-custom-format demonstrates ports.Pattern's CustomFormat
// escape hatch: FilePattern, CachePattern, and SocketPattern are normally
// locked to FileFormatKind{JSON, YAML, TOML} — CustomFormat lets a port
// declare ANY format.Format[T] instead (Gob, raw binary/PNG, or any custom
// marshal/unmarshal), closing the gap without waiting for go-codex to add
// a new enum value for every future wire format.
//
// Two scenes:
//   - FilePattern + format.Binary(pngCodec): a typed IOPort whose "file" is
//     a raw PNG blob — content stays binary, never JSON-wrapped.
//   - CachePattern + format.Gob(structCodec): a typed cache entry encoded
//     with encoding/gob instead of JSON — smaller, Go-native, no map
//     intermediate.
//
// # Running
//
// go run ./examples/pattern-custom-format
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Scene 1: FilePattern + format.Binary (raw PNG) ────────────────────────────

var pngCodec = codex.Bytes().
	Refine(validate.MaxBytes(5 * 1024 * 1024)).
	Refine(validate.PNG)

// ImageQuery selects which stored image to read/write.
type ImageQuery struct{ ID string }

var imageQueryCodec = codex.Struct[ImageQuery](
	codex.RequiredField("id", codex.String(),
		func(q ImageQuery) string { return q.ID },
		func(q *ImageQuery, v string) { q.ID = v },
	),
)

// newImagesPort declares the Images IOPort — a real file store rooted at
// dir. In a real service, dir would be a fixed directory declared once in
// domain/pipeline code (no runtime-computed path needed); the temp dir here
// is purely a self-contained-example concern.
//
// The port's "file" content is the RESPONSE type — []byte, codec-validated
// as a real PNG on every read AND write. Without CustomFormat, FilePattern
// would only offer JSON/YAML/TOML — wrapping the raw bytes in a JSON string
// (base64) instead of writing them as-is.
func newImagesPort(dir string) *ports.IOPort[ImageQuery, []byte] {
	return codex.Must(ports.NewIOPort[ImageQuery, []byte]("images",
		imageQueryCodec, pngCodec, ports.PortOptions{}))
}

// ── Scene 2: CachePattern + format.Gob (typed binary cache entry) ─────────────

// Session is a typed cache value — encoded with Gob instead of JSON.
type Session struct {
	UserID string
	Roles  []string
}

var sessionCodec = codex.Struct[Session](
	codex.RequiredField("user_id", codex.String().Refine(validate.NonEmptyString),
		func(s Session) string { return s.UserID },
		func(s *Session, v string) { s.UserID = v },
	),
	codex.OptionalField("roles", codex.SliceOf(codex.String()),
		func(s Session) []string { return s.Roles },
		func(s *Session, v []string) { s.Roles = v },
	),
)

// Sessions is an IOPort whose cached value is Gob-encoded — smaller wire
// size than JSON, no map[string]any intermediate, still fully
// codec-validated on every read and write.
var Sessions = codex.Must(ports.NewIOPort[ImageQuery, Session]("sessions",
	imageQueryCodec, sessionCodec, ports.PortOptions{}))

func main() {
	// ── Scene 1: raw PNG through a Pattern-declared FilePattern ───────────
	fmt.Println("─── Scene 1: FilePattern + format.Binary (raw PNG)")

	dir, err := os.MkdirTemp("", "pattern-custom-format")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	imgHandle := codex.Must(newImagesPort(dir).PluginFilePattern(ports.FilePattern{
		Path:         dir + "/{id}.png",
		CustomFormat: format.Binary(pngCodec).WithContentType("image/png"),
	}))
	pngSignature := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	fakePNG := append(append([]byte{}, pngSignature...), []byte("...pixels...")...)

	vars := map[string]string{"id": "avatar"}
	fileOpts := ports.FileOptions{}
	if _, err := imgHandle.Write(vars, fakePNG, fileOpts); err != nil {
		panic(err)
	}
	path, _ := imgHandle.BuildPath(vars)
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  wrote %d bytes; first 8 bytes are the PNG signature: %v\n",
		len(raw), raw[:8])
	fmt.Printf("  is raw binary (not JSON/base64-wrapped): %v\n",
		raw[0] == 0x89) // JSON would have started with a quote character

	got, err := imgHandle.Read(vars, fileOpts)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  read back %d bytes, matches original: %v\n", len(got), string(got) == string(fakePNG))

	// ── Scene 2: Gob-encoded cache entry through a Pattern-declared Cache ─
	fmt.Println("\n─── Scene 2: CachePattern + format.Gob (typed binary cache)")

	cacheHandle := codex.Must(Sessions.PluginCachePattern(ports.CachePattern{
		Key:          "session:{id}",
		CustomFormat: format.Gob(sessionCodec),
	}))
	sess := Session{UserID: "u-42", Roles: []string{"admin", "billing"}}
	encoded, err := cacheHandle.Format.Marshal(sess)
	if err != nil {
		panic(err)
	}
	decoded, err := cacheHandle.Format.Unmarshal(encoded)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  encoded %d bytes via Gob (key template: %q)\n", len(encoded), cacheHandle.Key)
	fmt.Printf("  round-trip: user=%s roles=%v\n", decoded.UserID, decoded.Roles)

	// ── Regression: an invalid PNG is rejected by the codec constraint ────
	fmt.Println("\n─── Constraint enforcement still applies to CustomFormat")
	if _, err := imgHandle.Write(vars, []byte("not a png"), fileOpts); err != nil {
		fmt.Println("  invalid PNG correctly rejected:", err)
	}
}
