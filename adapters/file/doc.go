// Package file provides protocol-agnostic file IO adapter bindings for the ports package.
//
// All adapters are stdlib-only (no external dependencies). They implement the
// [ports.SourceAdapter], [ports.SinkAdapter], and [ports.IOAdapter] interfaces and
// are wired to pipelines via [ports.SourcePort.Bind], [ports.SinkPort.Bind], and
// [ports.IOPort.Bind].
//
// Sources (use with [ports.SourcePort]):
//   - [ScanAdapter] — decodes a newline-delimited file (NDJSON, CSV, etc.) line by line
//   - [WatchAdapter] — emits file paths for new files created in a directory
//
// Intermediate (use with [ports.IOPort]):
//   - [ReadEachAdapter] — reads a complete typed file for each upstream item (enrichment)
//
// Sinks (use with [ports.SinkPort]):
//   - [DrainWriteAdapter] — encodes each item and writes it as a line to an [io.Writer]
//   - [DrainWriteFileAdapter] — writes each item as a complete typed file (whole-file overwrite)
//   - [DrainPatchAdapter] — applies each item as an untyped map[string]any partial
//     update (JSON Merge Patch semantics) via [ports.File.Patch]; map-based formats
//     (JSON/YAML/TOML/[format.New]) only
//   - [DrainPatchEncodedAdapter] — applies each item as a typed partial update via
//     [ports.PatchEncoded]; same map-based-format restriction
package file
