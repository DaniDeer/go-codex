// Package format bridges Codec[T] to concrete serialization formats.
//
// A [Codec][T] works with an intermediate representation (map[string]any) that
// is format-agnostic. Format wraps that intermediate layer so the same codec can
// read and write multiple wire formats without any changes to the codec itself.
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
