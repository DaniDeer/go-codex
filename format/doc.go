// Package format bridges Codec[T] to concrete serialization formats.
//
// A [Codec][T] works with an intermediate representation (map[string]any) that
// is format-agnostic. Format wraps that intermediate layer so the same codec can
// read and write multiple wire formats without any changes to the codec itself.
//
// # File I/O — Read, Write, Update, Patch, PatchEncoded
//
// [File][T] is a declarative typed file descriptor. It supports five operations:
//
//   - [File.Read]   — full decode (reads entire file into T)
//   - [File.Write]  — full encode (overwrites file with T); use when you already have the decoded value
//   - [File.Update] — typed read-modify-write: fn(T) T; use when you need the latest file state first
//   - [File.Patch]  — partial field update (map[string]any); unknown fields dropped
//   - [PatchEncoded] — typed partial update via a separate patch codec (free function);
//     fields in patchCodec but NOT in the file codec are preserved in the output
//
// # Field survival rules for Patch and PatchEncoded
//
// Every write operation filters output through its codec. The rules differ:
//
//	// Patch: only file-codec fields survive; unknown keys in the patch map are dropped
//	configFile.Patch(nil, map[string]any{"port": 9090}, opts)
//	// → file codec fields updated/re-written; "port" updated; unknown keys dropped
//
//	// PatchEncoded: patchCodec fields survive even if not in the file codec
//	format.PatchEncoded(configFile, nil, patchCodec, patchValue, opts)
//	// → file codec fields updated/re-written; patchCodec fields written (even extra ones)
//
// Field survival summary:
//
//	Field in file codec + field in patch map/patchCodec   → updated ✓
//	Field in file codec + absent from patch               → preserved ✓
//	Field in patchCodec only (not in file codec)          → written by PatchEncoded ✓
//	Field in neither codec                                → dropped by both Patch and PatchEncoded
//
// Key rule: use [PatchEncoded] to intentionally add new fields to a file by
// declaring them in the patch codec. Use [File.Patch] with an explicit
// map[string]any when unknown keys should be silently discarded.
//
// Patch and PatchEncoded are supported only for map-based formats (JSON, YAML, TOML, New).
// Check [Format.IsPatchable] before calling either when the format is not known at compile time.
//
// All file error types implement [slog.LogValuer] for structured logging:
//
//	var encErr format.FileEncodeError
//	if errors.As(err, &encErr) {
//	    slog.Warn("encode failed", "error", encErr)  // structured output via LogValue()
//	}
//
// # Embedded format codecs — JSON/YAML/TOML within a string field
//
// Some APIs and protocols store a serialised document inside a string field
// (CloudEvents data-as-string, database JSONB via REST, Kafka message headers,
// device-twin configuration). [EmbeddedJSON], [EmbeddedYAML], and [EmbeddedTOML]
// return a [codex.Codec][T] that treats the wire string as a nested document:
//
//	// Wire: {"event":"user.created","payload":"{\"id\":\"123\",\"name\":\"Alice\"}"}
//	var eventCodec = codex.Struct[Event](
//	    codex.RequiredField("event",   codex.String(), ...),
//	    codex.RequiredField("payload", format.EmbeddedJSON(userCodec), ...),
//	)
//
//	// Compose with File[T] — the outer format handles the file bytes;
//	// EmbeddedJSON handles the string-to-struct field conversion.
//	var eventFile = format.NewFile("events/user.json", format.JSON(eventCodec))
//	event, err := eventFile.Read(nil, format.FileOptions{})
//
// Decode: wire string → format unmarshal → inner.Decode → T
// Encode: inner.Encode → format marshal → wire string
//
// Format parse failures return [EmbeddedDecodeError]{Format, Err};
// marshal failures return [EmbeddedEncodeError]{Format, Err}.
// Both implement [slog.LogValuer]. Inner codec validation errors propagate unchanged.
//
// # Built-in formats
//
// Text-based formats (JSON, YAML, TOML) pass through the map[string]any intermediate:
//
//	format.JSON(codec)  // application/json
//	format.YAML(codec)  // application/yaml
//	format.TOML(codec)  // application/toml
//
// # Binary formats
//
// Binary formats bypass the intermediate and operate on the typed value directly:
//
//   - [Gob] — uses encoding/gob framing. Suitable for Go-to-Go communication and
//     binary caching. NOT suitable for writing files that must be readable by other
//     tools (image viewers, PDF readers), because Gob adds its own framing bytes.
//
//   - [Binary] — writes and reads []byte as-is, without any encoding. Suitable for
//     raw binary file I/O (PNG, JPEG, PDF, WAV…) and HTTP binary bodies. Unlike Gob,
//     Binary produces files that any tool understanding the underlying format can open.
//
// # Choosing between Binary and Gob
//
//   - Use [Binary] when the file must be byte-identical to the original format (PNG, PDF…).
//   - Use [Gob] for internal Go-to-Go serialization where framing overhead is acceptable.
//
// # Custom formats
//
// For formats not covered by the built-ins, use:
//
//   - [New] — map-based intermediate (CBOR, MessagePack, XML, …)
//   - [NewTyped] — typed T directly, without map[string]any (templ HTML, Protobuf, image.Image)
//   - [NewStreamed] — streams to io.Writer without buffering (SSR streaming, chunked exports)
//
// [Binary] is convenience sugar over [NewTyped][[]byte] with identity marshal/unmarshal.
// Use [NewTyped] directly when T ≠ []byte or when marshal/unmarshal perform real encoding.
package format
