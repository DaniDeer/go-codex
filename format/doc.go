// Package format bridges Codec[T] to concrete serialization formats.
//
// A [Codec][T] works with an intermediate representation (map[string]any) that
// is format-agnostic. Format wraps that intermediate layer so the same codec can
// read and write multiple wire formats without any changes to the codec itself.
//
// # File I/O has moved
//
// The declarative typed file descriptor (File[T], NewFile, Read/Write/Update/
// Patch, PatchEncoded, FilePathParam, and the file error types) now lives in
// the ports package — see [ports.File] — since it is a protocol-agnostic
// addressing descriptor bound via [ports.FilePattern] to adapters/file, the
// same role [ports.Cache] plays for adapters/redis. This package still
// provides [Format.IsPatchable], [Format.PatchInto], [Format.Codec],
// [Format.UnmarshalRaw], [Format.MarshalRaw], and [DeepMerge] — the
// lower-level primitives [ports.File.Patch]/[ports.PatchEncoded] are built
// on.
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
//	// Compose with ports.File[T] — the outer format handles the file bytes;
//	// EmbeddedJSON handles the string-to-struct field conversion.
//	var eventFile = ports.NewFile("events/user.json", format.JSON(eventCodec))
//	event, err := eventFile.Read(nil, ports.FileOptions{})
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
