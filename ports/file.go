package ports

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/templatematch"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/stats"
)

// ── FilePathParam ─────────────────────────────────────────────────────────────

// FilePathParam describes a {varName} placeholder in a [File] path template.
// It mirrors [TopicParam] and [PathParam] — no Required field because every
// template variable must always be present.
//
// FilePathParam implements the [FileOpt] interface: pass it directly to [NewFile].
type FilePathParam struct {
	// Name is the placeholder name (without braces) in the path template.
	// e.g. for template "data/{date}/{sensor}.json", Name is "date" or "sensor".
	Name string

	// Description enriches the documentation for this path variable.
	Description string

	// Codec validates the variable value at [File.BuildPath] and [File.Read]/
	// [File.Write]/[File.Update] time. When non-nil, the codec's schema is
	// available via [File.PathParamSchemas]. Nil means no runtime validation.
	Codec *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated FilePathParam.
// Use this instead of setting Codec directly:
//
//	ports.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date))
func (p FilePathParam) WithCodec(c codex.Codec[string]) FilePathParam {
	p.Codec = &c
	return p
}

func (p FilePathParam) applyFile(fb *fileBuilder) { fb.params = append(fb.params, p) }

// MergedFilePathParam is returned by [NewFilePathParam]. It embeds the
// unchanged [FilePathParam] (spec/validation, exactly as before) plus a
// merge field produced internally via [codex.RequiredField], so the same
// declaration serves both [File.BuildPath]/[File.MatchPath] validation AND
// [File.MergeFields] / [codex.DecodeVars] merging.
type MergedFilePathParam[T any] struct {
	FilePathParam
	field codex.FieldCodec[T]
}

// NewFilePathParam declares a path template variable that is BOTH validated
// against codec (exactly like plain [FilePathParam], unchanged spec/
// validation behavior) AND merged into T by [File.MergeFields] /
// [codex.DecodeVars] — one declaration instead of a FilePathParam plus a
// separate codex.Field.
//
// NewFilePathParam is the PRIMARY, recommended way to declare a File path
// variable. The plain [FilePathParam] struct literal remains available as
// the low-level escape hatch for validate-only variables with no merge
// need (avoids forcing a get/set pair on a variable the caller never reads
// directly).
//
//	var readingFile = ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(valueOnlyCodec),
//	    ports.NewFilePathParam("sensorID", codex.String().Refine(validate.NonEmptyString),
//	        func(r ReadingMeta) string { return r.SensorID },
//	        func(r *ReadingMeta, v string) { r.SensorID = v }),
//	)
func NewFilePathParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedFilePathParam[T] {
	return MergedFilePathParam[T]{
		FilePathParam: FilePathParam{Name: name, Codec: &codec},
		field:         codex.RequiredField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value, mirroring FilePathParam.WithCodec's existing chain style.
func (p MergedFilePathParam[T]) WithDescription(desc string) MergedFilePathParam[T] {
	p.Description = desc
	return p
}

func (p MergedFilePathParam[T]) applyFile(fb *fileBuilder) {
	fb.params = append(fb.params, p.FilePathParam) // unchanged spec/validation path
	fb.mergeFields = append(fb.mergeFields, p.field)
}

// ── FileOptions ───────────────────────────────────────────────────────────────

// FileOptions configures the behaviour of [File.Read], [File.ReadMerged],
// [File.Write], [File.Update], [File.Delete], [File.Patch], [PatchEncoded],
// and [WriteHandle]. Not every field applies to every method — e.g. Perm/
// CreateDirs/DirPerm only affect Write-family methods, DryRun/Strict apply
// per-method with a method-specific meaning documented on each field below.
type FileOptions struct {
	// Observer, when non-nil, receives per-operation lifecycle events.
	// [stats.FileObserver.RecordFileRead] is called after every Read or Update
	// (read phase). [stats.FileObserver.RecordFileWrite] is called after every
	// Write or Update (write phase). Per-field decode/encode errors are reported
	// via [stats.Observer.RecordValidationError] with location "file".
	//
	// The observer is type-asserted to [stats.FileObserver] — existing Observer
	// implementations need not implement FileObserver. Defaults to
	// [stats.NoopObserver] when nil.
	Observer stats.Observer

	// Perm is the file permission used when creating a new file.
	// Defaults to 0644 when zero.
	Perm os.FileMode

	// Context is an optional context for TraceObserver span parent propagation.
	// When non-nil, file operations create child spans under the trace span
	// carried by this context. When nil (default), spans use [context.Background]
	// and become root spans.
	Context context.Context

	// CreateDirs, when true, creates any missing parent directories (via
	// [os.MkdirAll]) before [File.Write] writes the file. Default false —
	// matches [os.WriteFile]'s own behavior exactly (it creates the FILE if
	// missing, but never its parent directories), so existing callers see
	// no change unless they opt in.
	CreateDirs bool

	// DirPerm is the permission used for directories created by CreateDirs.
	// Defaults to 0755 when zero. Has no effect when CreateDirs is false.
	DirPerm os.FileMode

	// DryRun, when true:
	//   - on [File.Delete]: reports what WOULD happen (via existed/err)
	//     WITHOUT removing the file.
	//   - on [File.Write]: reports which directories WOULD be created
	//     (via createdDirs) WITHOUT creating them or writing the file —
	//     the value is still encoded, so encode errors still surface.
	// Default false.
	DryRun bool

	// Strict, when true:
	//   - on [File.Delete]: requires the file to have existed —
	//     [FileNotFoundError] instead of idempotent "ensure absent" success.
	//   - on [File.Write]: requires the file to NOT already exist —
	//     [FileAlreadyExistsError] instead of silently overwriting
	//     (O_CREATE|O_EXCL semantics; the create-side precondition-mirror
	//     of Delete's Strict). Has no effect on Read/Update/Patch.
	// Default false — existing callers see no change unless they opt in.
	Strict bool
}

// ── File ──────────────────────────────────────────────────────────────────────

// File is a declarative typed file descriptor: a path template, a wire format,
// and optional per-variable codecs. It bundles everything needed to read, write,
// and update a file in one reusable value.
//
// File mirrors the declare-once pattern of [api/rest.Route] and
// [api/events.Channel]:
//
//   - [NewFile] declares the file descriptor as a value — no side effects.
//   - [File.Read], [File.Write], [File.Update] perform the I/O.
//   - [File.BuildPath] substitutes variables and validates without any I/O.
//
// For static paths (no template variables), pass nil for vars in all methods.
//
// Typical usage:
//
//	// Declare once — share across functions and packages.
//	var configFile = ports.NewFile("config.toml", format.TOML(configCodec))
//
//	// Read
//	cfg, err := configFile.Read(nil, ports.FileOptions{Observer: obs})
//
//	// Update (read-modify-write)
//	err = configFile.Update(nil, func(c Config) Config {
//		c.Port = 9090
//		return c
//	}, ports.FileOptions{Observer: obs})
//
//	// Template path with variable validation
//	var measurementFile = ports.NewFile("data/{date}/{sensorID}.json",
//	    format.JSON(measurementCodec),
//	    ports.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
//	    ports.FilePathParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
//	)
//
//	path, err := measurementFile.BuildPath(map[string]string{
//	    "date":     "2024-01-15",
//	    "sensorID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
//	})
//	// path == "data/2024-01-15/f47ac10b-58cc-4372-a567-0e02b2c3d479.json"
type File[T any] struct {
	// Template is the original path template (with {varName} placeholders).
	Template string

	format      format.Format[T]
	params      []FilePathParam
	mergeFields []codex.FieldCodec[T]
}

type fileBuilder struct {
	params []FilePathParam
	// mergeFields holds type-erased codex.FieldCodec[T] values registered
	// via NewFilePathParam. T is asserted in NewFile[T], where T is already
	// known from the format.Format[T] argument.
	mergeFields []any
}

// FileOpt is the sealed option interface for [NewFile].
type FileOpt interface{ applyFile(*fileBuilder) }

// NewFile creates a [File] descriptor from a path template, a wire format, and
// optional [FilePathParam] values.
//
// NewFile is infallible — it only captures the spec. Validation of template
// variable names against registered params runs at [File.BuildPath] time.
//
// NewFile PANICS if a merge field registered via [NewFilePathParam] does
// not match T — this can only happen if a caller manually constructs a
// [MergedFilePathParam] with the wrong type parameter, a programming error
// analogous to the ones [forge.NewFunction] already panics on (empty name/
// version), not a runtime data error. NewFile also PANICS if template
// contains more than one "**" glob segment — see
// [internal/templatematch.ValidateGlobstarCount] — another structural
// template error caught at declaration time.
func NewFile[T any](template string, f format.Format[T], opts ...FileOpt) File[T] {
	if err := templatematch.ValidateGlobstarCount(template); err != nil {
		panic("ports: NewFile: " + err.Error())
	}
	var fb fileBuilder
	for _, opt := range opts {
		opt.applyFile(&fb)
	}
	mergeFields := make([]codex.FieldCodec[T], len(fb.mergeFields))
	for i, mf := range fb.mergeFields {
		fc, ok := mf.(codex.FieldCodec[T])
		if !ok {
			panic(fmt.Sprintf("ports: NewFile[%T]: merge field %d has the wrong type parameter (got %T)", *new(T), i, mf))
		}
		mergeFields[i] = fc
	}
	return File[T]{
		Template:    template,
		format:      f,
		params:      fb.params,
		mergeFields: mergeFields,
	}
}

// MergeFields returns the merge-capable fields registered via
// [NewFilePathParam] — feed them directly into [codex.DecodeVars] /
// [codex.EncodeVars], or use the bundling convenience built on top:
// [File.ReadMerged] (decode-merge) and [WriteHandle] (encode-side
// single-call convenience):
//
//	vars, _ := readingFile.MatchPath(path)
//	var meta ReadingMeta
//	err := codex.DecodeVars(&meta, vars, readingFile.MergeFields()...)
//	// or, in one call: meta, err := readingFile.ReadMerged(vars, opts)
func (fh File[T]) MergeFields() []codex.FieldCodec[T] {
	return fh.mergeFields
}

// BuildPath substitutes {varName} placeholders with the values in vars and
// validates each against its registered [FilePathParam.Codec]. Returns the
// concrete file path on success.
//
// All template variables must be present in vars; missing variables return a
// [MissingFilePathVarError]. Values are validated before substitution; codec
// failures return a [FilePathParamError].
//
// When vars is nil or empty and the template has no placeholders, BuildPath
// returns the template unchanged.
//
// Returns [FileWildcardBuildError] if the template is glob-enabled (see
// [internal/templatematch.IsGlobEnabled]) — a glob-enabled template does
// not describe a single concrete path.
func (fh File[T]) BuildPath(vars map[string]string) (string, error) {
	if templatematch.IsGlobEnabled(fh.Template) {
		return "", FileWildcardBuildError{Template: fh.Template}
	}
	codecMap := make(map[string]*codex.Codec[string], len(fh.params))
	for i := range fh.params {
		if fh.params[i].Codec != nil {
			codecMap[fh.params[i].Name] = fh.params[i].Codec
		}
	}
	return buildFromFileTemplate(fh.Template, vars, codecMap)
}

// ValidatePathVars validates the variable values in vars against their
// registered codecs without building the concrete path. Returns the first
// codec failure as a [FilePathParamError], or [MissingFilePathVarError] for
// absent variables.
func (fh File[T]) ValidatePathVars(vars map[string]string) error {
	for i := range fh.params {
		p := &fh.params[i]
		if p.Codec == nil {
			continue
		}
		value, ok := vars[p.Name]
		if !ok {
			return MissingFilePathVarError{Name: p.Name}
		}
		if err := p.Codec.Validate(value); err != nil {
			return FilePathParamError{Name: p.Name, Value: value, Err: err}
		}
	}
	return nil
}

// PathParamSchemas returns a map from template variable name to the codec's
// [schema.Schema] for each [FilePathParam] that has a [FilePathParam.Codec] set.
// Parameters without a codec are omitted. Returns an empty map when no params
// have codecs registered.
//
// Use this for documentation generation or spec tooling that needs the schema
// of each path variable (e.g. emitting a machine-readable description of an
// API that reads or writes files with templated paths).
func (fh File[T]) PathParamSchemas() map[string]schema.Schema {
	out := make(map[string]schema.Schema, len(fh.params))
	for _, p := range fh.params {
		if p.Codec != nil {
			out[p.Name] = p.Codec.Schema
		}
	}
	return out
}

// MatchPath is the inverse of [File.BuildPath]: it matches a concrete,
// already-discovered file path (e.g. from the caller's own
// filepath.WalkDir/filepath.Glob) against the File's path template and
// returns the extracted variable values, validated against each
// registered [FilePathParam.Codec] — mirrors [ports.File.BuildPath]'s
// forward direction, and the same pattern used by adapters/mqtt's
// TopicVarsFromMessage for MQTT topics.
//
// A {varName} placeholder captures everything up to the next "/" and MAY
// share a segment with literal text — e.g. the template
// "readings/{sensorID}/{date}.json" correctly extracts "2024-01-15" (not
// "2024-01-15.json") from "readings/sensor-42/2024-01-15.json".
//
// Returns [FilePathMismatchError] if path does not match the template's
// structure (wrong number of segments, or literal text does not match).
// Returns [FilePathParamError] if an extracted variable fails its
// registered codec.
//
//	vars, err := readingFile.MatchPath("readings/sensor-42/2024-01-15.json")
//	// vars == map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"}
//
// MatchPath ALSO supports a glob-enabled template (see
// [internal/templatematch.IsGlobEnabled]) — "**"/"*"/"?"/"[...]" segments
// match anonymously (never captured), useful for validating/extracting
// vars from a path already discovered via [Dir.List]'s glob-discovery
// mode or the caller's own filepath.WalkDir.
func (fh File[T]) MatchPath(path string) (map[string]string, error) {
	var vars map[string]string
	var err error
	if templatematch.IsGlobEnabled(fh.Template) {
		vars, err = templatematch.MatchGlob(fh.Template, path, func(template, path string) error {
			return FilePathMismatchError{Template: template, Path: path}
		})
	} else {
		vars, err = matchFileTemplate(fh.Template, path)
	}
	if err != nil {
		return nil, err
	}
	for i := range fh.params {
		p := &fh.params[i]
		if p.Codec == nil {
			continue
		}
		value := vars[p.Name]
		if err := p.Codec.Validate(value); err != nil {
			return nil, FilePathParamError{Name: p.Name, Value: value, Err: err}
		}
	}
	return vars, nil
}

// Read builds the concrete path from vars, reads the file, and decodes its
// contents using the file's format codec.
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileReadError] — os.ReadFile failure
//   - [FileDecodeError] — format decode/validation failure (wraps [codex.ValidationErrors])
func (fh File[T]) Read(vars map[string]string, opts FileOptions) (T, error) {
	var zero T
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	path, err := fh.BuildPath(vars)
	if err != nil {
		return zero, err // FilePathParamError or MissingFilePathVarError — no I/O
	}

	var opErr error
	traceCtx := opts.Context
	if traceCtx == nil {
		traceCtx = context.Background()
	}
	if to, ok := obs.(stats.TraceObserver); ok {
		traceCtx = to.StartSpan(traceCtx, "file.read", path)
		defer func() { to.EndSpan(traceCtx, opErr) }()
	}

	start := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		opErr = FileReadError{Path: path, Err: err}
		recordFileRead(obs, path, false, time.Since(start))
		return zero, opErr
	}

	v, err := fh.format.Unmarshal(data)
	if err != nil {
		opErr = FileDecodeError{Path: path, Err: err}
		stats.ReportErrors(obs, "file", err)
		recordFileRead(obs, path, false, time.Since(start))
		return zero, opErr
	}

	recordFileRead(obs, path, true, time.Since(start))
	return v, nil
}

// ReadMerged is the decode-merge convenience: it reads and decodes exactly
// like [File.Read], then ADDITIONALLY merges vars into the SAME returned
// value via [codex.DecodeVars], using the merge-capable fields registered
// via [NewFilePathParam] — mirrors [events.ChannelHandle.DecodeMerged]/
// [reqreply.RouteHandle.DecodeMerged] for the file boundary.
//
// Additive — [File.Read] is unchanged; ReadMerged behaves identically to a
// bare Read when the file declares no merge-capable path params
// ([File.MergeFields] is empty).
//
// Example — template path "readings/{sensorID}/data.json" declared with
// [NewFilePathParam], so the extracted sensorID is merged into the
// returned struct's own field:
//
//	reading, err := readingFile.ReadMerged(map[string]string{"sensorID": id}, ports.FileOptions{})
//	// reading.SensorID == id, no manual assignment needed.
func (fh File[T]) ReadMerged(vars map[string]string, opts FileOptions) (T, error) {
	v, err := fh.Read(vars, opts)
	if err != nil {
		return v, err
	}
	if len(fh.mergeFields) == 0 {
		return v, nil
	}
	if err := codex.DecodeVars(&v, vars, fh.mergeFields...); err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

// Write builds the concrete path from vars, encodes v, and writes it to the file.
// The file is created if it does not exist, or truncated and overwritten if it does
// — unless [FileOptions.Strict] is set AND the file already exists, in which case
// Write returns [FileAlreadyExistsError] and performs no write (O_CREATE|O_EXCL
// semantics). Missing PARENT directories are NOT created unless
// [FileOptions.CreateDirs] is set — matches [os.WriteFile]'s own behavior by default.
//
// createdDirs lists the parent directories that were created (or, under
// [FileOptions.DryRun], that WOULD be created) — empty when CreateDirs is
// false or no parent directories were missing. Under DryRun, NOTHING is
// created or written — createdDirs and any [FileAlreadyExistsError]
// reflect exactly what a real call would do (the value is still encoded,
// so [FileEncodeError] still surfaces normally).
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileEncodeError] — format encode/validation failure
//   - [FileAlreadyExistsError] — file already exists AND [FileOptions.Strict] is set
//   - [FileWriteError] — os.MkdirAll (when CreateDirs is set) or os.WriteFile failure
func (fh File[T]) Write(vars map[string]string, v T, opts FileOptions) (createdDirs []string, err error) {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	dirPerm := opts.DirPerm
	if dirPerm == 0 {
		dirPerm = 0755
	}

	path, buildErr := fh.BuildPath(vars)
	if buildErr != nil {
		return nil, buildErr
	}

	var opErr error
	traceCtx := opts.Context
	if traceCtx == nil {
		traceCtx = context.Background()
	}
	if to, ok := obs.(stats.TraceObserver); ok {
		traceCtx = to.StartSpan(traceCtx, "file.write", path)
		defer func() { to.EndSpan(traceCtx, opErr) }()
	}

	data, encErr := fh.format.Marshal(v)
	if encErr != nil {
		opErr = FileEncodeError{Path: path, Err: encErr}
		stats.ReportErrors(obs, "file", encErr)
		recordFileWrite(obs, path, false, 0)
		return nil, opErr
	}

	start := time.Now()

	if opts.CreateDirs {
		createdDirs = missingParentDirs(filepath.Dir(path))
	}

	if opts.DryRun {
		if opts.Strict {
			if _, statErr := os.Stat(path); statErr == nil {
				opErr = FileAlreadyExistsError{Path: path}
				recordFileWrite(obs, path, false, time.Since(start))
				return createdDirs, opErr
			} else if !os.IsNotExist(statErr) {
				opErr = FileWriteError{Path: path, Err: statErr}
				recordFileWrite(obs, path, false, time.Since(start))
				return createdDirs, opErr
			}
		}
		recordFileWrite(obs, path, true, time.Since(start))
		return createdDirs, nil
	}

	if opts.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
			opErr = FileWriteError{Path: path, Err: err}
			recordFileWrite(obs, path, false, time.Since(start))
			return nil, opErr
		}
	}

	if opts.Strict {
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if openErr != nil {
			if os.IsExist(openErr) {
				opErr = FileAlreadyExistsError{Path: path}
			} else {
				opErr = FileWriteError{Path: path, Err: openErr}
			}
			recordFileWrite(obs, path, false, time.Since(start))
			return createdDirs, opErr
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			opErr = FileWriteError{Path: path, Err: writeErr}
			recordFileWrite(obs, path, false, time.Since(start))
			return createdDirs, opErr
		}
		if closeErr != nil {
			opErr = FileWriteError{Path: path, Err: closeErr}
			recordFileWrite(obs, path, false, time.Since(start))
			return createdDirs, opErr
		}
		recordFileWrite(obs, path, true, time.Since(start))
		return createdDirs, nil
	}

	if err := os.WriteFile(path, data, perm); err != nil {
		opErr = FileWriteError{Path: path, Err: err}
		recordFileWrite(obs, path, false, time.Since(start))
		return createdDirs, opErr
	}

	recordFileWrite(obs, path, true, time.Since(start))
	return createdDirs, nil
}

// Delete removes the file at the built path. By default, Delete is
// idempotent "ensure absent" semantics: if the file is already gone,
// Delete succeeds. Set [FileOptions.Strict] to require the file to have
// existed — a missing file then returns [FileNotFoundError] instead.
// existed reports whether the file was present before the call (true
// even under [FileOptions.DryRun], which performs the same existence
// check but skips the actual os.Remove).
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileNotFoundError] — file did not exist AND [FileOptions.Strict] is set
//   - [FileDeleteError] — os.Remove failure OTHER than "already absent"
//     (e.g. permission denied, path is actually a non-empty directory)
func (fh File[T]) Delete(vars map[string]string, opts FileOptions) (existed bool, err error) {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	path, buildErr := fh.BuildPath(vars)
	if buildErr != nil {
		return false, buildErr
	}

	var opErr error
	traceCtx := opts.Context
	if traceCtx == nil {
		traceCtx = context.Background()
	}
	if to, ok := obs.(stats.TraceObserver); ok {
		traceCtx = to.StartSpan(traceCtx, "file.delete", path)
		defer func() { to.EndSpan(traceCtx, opErr) }()
	}

	start := time.Now()

	if opts.DryRun {
		_, statErr := os.Stat(path)
		existed = statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			opErr = FileDeleteError{Path: path, Err: statErr}
			recordFileDelete(obs, path, false, time.Since(start))
			return false, opErr
		}
		if !existed && opts.Strict {
			opErr = FileNotFoundError{Path: path}
			recordFileDelete(obs, path, false, time.Since(start))
			return false, opErr
		}
		recordFileDelete(obs, path, true, time.Since(start))
		return existed, nil
	}

	removeErr := os.Remove(path)
	if removeErr == nil {
		recordFileDelete(obs, path, true, time.Since(start))
		return true, nil
	}
	if os.IsNotExist(removeErr) {
		if opts.Strict {
			opErr = FileNotFoundError{Path: path}
			recordFileDelete(obs, path, false, time.Since(start))
			return false, opErr
		}
		recordFileDelete(obs, path, true, time.Since(start))
		return false, nil
	}
	opErr = FileDeleteError{Path: path, Err: removeErr}
	recordFileDelete(obs, path, false, time.Since(start))
	return true, opErr
}

// WriteHandle is the single-call convenience wrapper around [File.Write]:
// it derives the path vars from v automatically via
// [codex.EncodeVars](v, [File.MergeFields]()...) — one struct in, no
// manual vars map — mirroring [mqtt5.PublishHandle]/[nethttp.CallHandle]'s
// convenience for MQTT 5 events / REST clients.
//
// [File.Write] remains available as the lower-level escape hatch for
// callers that build the vars map themselves (e.g. no merge-capable path
// params declared, or vars come from a non-struct source).
//
//	createdDirs, err := ports.WriteHandle(readingFile, reading, ports.FileOptions{})
//	// path derived from reading's own SensorID field — no manual vars map.
func WriteHandle[T any](fh File[T], v T, opts FileOptions) (createdDirs []string, err error) {
	vars, err := codex.EncodeVars(v, fh.mergeFields...)
	if err != nil {
		return nil, err
	}
	if len(vars) == 0 {
		vars = nil
	}
	return fh.Write(vars, v, opts)
}

// Update reads the file at the path built from vars, applies fn to the decoded
// value, then writes the result back. It is equivalent to Read followed by Write.
//
// Use Update when you need the current file contents to decide what to write —
// for example, incrementing a counter or conditionally modifying a field.
// If you already have the decoded value in memory, use [File.Write] directly
// to avoid an unnecessary re-read.
//
// Errors: see [File.Read] and [File.Write].
func (fh File[T]) Update(vars map[string]string, fn func(T) T, opts FileOptions) (createdDirs []string, err error) {
	v, err := fh.Read(vars, opts)
	if err != nil {
		return nil, err
	}
	return fh.Write(vars, fn(v), opts)
}

// PatchEncoded encodes patch using patchCodec and merges the result into the
// existing file, preserving fields not covered by either codec.
//
// PatchEncoded is a free function (not a method on File) because Go methods
// on a generic type cannot introduce additional type parameters. P is the
// patch type — a struct that contains only the fields you want to update.
// T is the full file type; it does not need to match P.
//
// # Field survival rules
//
//   - Fields in the file codec (T): re-written with their current values and validated.
//   - Fields in patchCodec (P) but NOT in the file codec: written to the file as-is.
//     These are validated by patchCodec before being merged, so they are safe to persist.
//   - Fields in the existing file that are in neither codec: dropped (no codec validates them).
//
// This makes PatchEncoded the right tool for intentionally adding new fields to a
// file — declare them in the patch codec and they will be persisted.
//
// Declare separate codecs for the file type and the patch type:
//
//	type AppConfig struct { Port int; LogLevel string; MaxWorkers int }
//	var configFile = ports.NewFile("config.json", format.JSON(appConfigCodec))
//
//	// Patch type — only patchable fields; may include fields not in AppConfig
//	type AppConfigPatch struct { Port int; LogLevel string; NewFeatureFlag bool }
//	var configPatchCodec = codex.Struct[AppConfigPatch](
//	    codex.RequiredField("port",
//	        codex.Int().Refine(validate.RangeInt(1, 65535)),
//	        func(p AppConfigPatch) int { return p.Port },
//	        func(p *AppConfigPatch, v int) { p.Port = v },
//	    ),
//	    codex.RequiredField("log_level",
//	        codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
//	        func(p AppConfigPatch) string { return p.LogLevel },
//	        func(p *AppConfigPatch, v string) { p.LogLevel = v },
//	    ),
//	    codex.RequiredField("new_feature_flag",
//	        codex.Bool(),
//	        func(p AppConfigPatch) bool { return p.NewFeatureFlag },
//	        func(p *AppConfigPatch, v bool) { p.NewFeatureFlag = v },
//	    ),
//	)
//
//	// new_feature_flag is not in AppConfig but IS in patchCodec — it will be written
//	err = ports.PatchEncoded(configFile, nil, configPatchCodec,
//	    AppConfigPatch{Port: 9090, LogLevel: "debug", NewFeatureFlag: true},
//	    ports.FileOptions{Observer: obs},
//	)
//
// PatchEncoded returns [FilePatchNotSupportedError] when the encoded
// intermediate is not a map[string]any (scalar or slice patch codec, or
// Gob/Binary format).
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation (no I/O)
//   - [FileEncodeError] — patchCodec.Encode(patch) fails (Refine constraint violation)
//   - [FilePatchNotSupportedError] — encoded intermediate is not map[string]any, or format not patchable
//   - [FileReadError] — os.ReadFile failure
//   - [FileDecodeError] — merged result fails fh's codec constraints for known fields
//   - [FileWriteError] — os.WriteFile failure
func PatchEncoded[T, P any](fh File[T], vars map[string]string, patchCodec codex.Codec[P], patch P, opts FileOptions) error {
	path, err := fh.BuildPath(vars)
	if err != nil {
		return err
	}

	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	// Fail fast before any I/O when the format doesn't support patching.
	if !fh.format.IsPatchable() {
		return FilePatchNotSupportedError{Path: path}
	}

	// 1. Encode the patch value using the patch codec — validates Refine constraints.
	intermediate, err := patchCodec.Encode(patch)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordFileWrite(obs, path, false, 0)
		return FileEncodeError{Path: path, Err: err}
	}
	patchMap, ok := intermediate.(map[string]any)
	if !ok {
		// Scalar, slice, or other non-struct codecs produce a non-map intermediate.
		return FilePatchNotSupportedError{Path: path}
	}

	var opErr error
	traceCtx := opts.Context
	if traceCtx == nil {
		traceCtx = context.Background()
	}
	if to, ok := obs.(stats.TraceObserver); ok {
		traceCtx = to.StartSpan(traceCtx, "file.write", path)
		defer func() { to.EndSpan(traceCtx, opErr) }()
	}

	// 2. Read the existing file.
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	readStart := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		opErr = FileReadError{Path: path, Err: err}
		recordFileRead(obs, path, false, time.Since(readStart))
		return opErr
	}

	// 3. Unmarshal existing bytes to intermediate map.
	raw, err := fh.format.UnmarshalRaw(data)
	if err != nil {
		opErr = FileDecodeError{Path: path, Err: err}
		recordFileRead(obs, path, false, time.Since(readStart))
		return opErr
	}
	existingMap, ok := raw.(map[string]any)
	if !ok {
		opErr = FileDecodeError{Path: path, Err: fmt.Errorf("existing file intermediate is not a map (got %T)", raw)}
		recordFileRead(obs, path, false, time.Since(readStart))
		return opErr
	}

	// 4. Deep-merge patchMap into existingMap.
	format.DeepMerge(existingMap, patchMap)

	// 5. Validate the file codec's known fields in the merged map.
	//    Unknown fields (from patchCodec or the existing file outside either codec)
	//    are ignored by Decode and are NOT dropped here — they survive in existingMap.
	if _, err := fh.format.Codec().Decode(existingMap); err != nil {
		stats.ReportErrors(obs, "file", err)
		opErr = FileDecodeError{Path: path, Err: err}
		recordFileRead(obs, path, false, time.Since(readStart))
		return opErr
	}
	recordFileRead(obs, path, true, time.Since(readStart))

	// 6. Marshal the merged map directly — NOT re-encode T.
	//    This preserves fields declared in patchCodec even if they are absent
	//    from the file codec (T).
	out, err := fh.format.MarshalRaw(existingMap)
	if err != nil {
		opErr = FileEncodeError{Path: path, Err: err}
		recordFileWrite(obs, path, false, 0)
		return opErr
	}

	// 7. Write.
	writeStart := time.Now()
	if err := os.WriteFile(path, out, perm); err != nil {
		opErr = FileWriteError{Path: path, Err: err}
		recordFileWrite(obs, path, false, time.Since(writeStart))
		return opErr
	}

	recordFileWrite(obs, path, true, time.Since(writeStart))
	return nil
}

// Patch reads the existing file, deep-patches its intermediate representation
// with the provided patch map, validates the result through the codec, and
// writes it back.
//
// patch follows JSON Merge Patch semantics (RFC 7396): keys present in patch
// overwrite the corresponding fields in the file; fields absent from patch are
// preserved through the read phase but dropped when the merged value is
// re-encoded through the file's codec. Only fields the codec knows about
// survive in the written file — unknown fields are silently dropped.
//
// To intentionally write fields not declared in the file's codec, use
// [PatchEncoded] with a patch codec that declares those fields explicitly.
//
// Patch is supported only for map-based formats: JSON, YAML, TOML, and formats
// created with [New]. Returns [FilePatchNotSupportedError] before any I/O for
// Gob, Binary, [NewTyped], and [NewStreamed] formats — check [Format.IsPatchable]
// upfront when the format type is not known at compile time.
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FilePatchNotSupportedError] — format does not use a map[string]any intermediate (no I/O)
//   - [FileReadError] — os.ReadFile failure
//   - [FileDecodeError] — patched result fails codec constraint validation
//   - [FileEncodeError] — encode failure after patch
//   - [FileWriteError] — os.WriteFile failure
func (fh File[T]) Patch(vars map[string]string, patch map[string]any, opts FileOptions) error {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	path, err := fh.BuildPath(vars)
	if err != nil {
		return err // FilePathParamError or MissingFilePathVarError — no I/O
	}

	// Fail fast before any I/O when the format doesn't support patching.
	if !fh.format.IsPatchable() {
		return FilePatchNotSupportedError{Path: path}
	}

	var opErr error
	traceCtx := opts.Context
	if traceCtx == nil {
		traceCtx = context.Background()
	}
	if to, ok := obs.(stats.TraceObserver); ok {
		traceCtx = to.StartSpan(traceCtx, "file.write", path)
		defer func() { to.EndSpan(traceCtx, opErr) }()
	}

	// Read the raw bytes.
	start := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		opErr = FileReadError{Path: path, Err: err}
		recordFileRead(obs, path, false, time.Since(start))
		return opErr
	}

	// Parse existing bytes, apply patch, validate through codec.
	patched, err := fh.format.PatchInto(data, patch)
	if err != nil {
		opErr = FileDecodeError{Path: path, Err: err}
		stats.ReportErrors(obs, "file", err)
		recordFileRead(obs, path, false, time.Since(start))
		return opErr
	}
	recordFileRead(obs, path, true, time.Since(start))

	// Write the patched value back. createdDirs is discarded — Patch
	// requires the file to already exist (it just Read it above), so
	// CreateDirs never has any missing parent directory left to create
	// by the time Write is reached here.
	_, err = fh.Write(vars, patched, opts)
	return err
}

// ── FilePatchNotSupportedError ────────────────────────────────────────────────

// FilePatchNotSupportedError is returned by [File.Patch] when the file's format
// does not use a [map[string]any] intermediate. Only JSON, YAML, TOML, and
// formats created with [New] support patching.
//
// Use [errors.As] to extract the path:
//
//	var patchErr ports.FilePatchNotSupportedError
//	if errors.As(err, &patchErr) {
//	    slog.Warn("patch not supported", "error", patchErr)
//	}
type FilePatchNotSupportedError struct {
	// Path is the concrete file path after template substitution.
	Path string
}

func (e FilePatchNotSupportedError) Error() string {
	return fmt.Sprintf("patch file %q: format does not support partial patch (use JSON, YAML, or TOML)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FilePatchNotSupportedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// ── Observer helpers ──────────────────────────────────────────────────────────

func recordFileRead(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileRead(path, success, d)
	}
}

func recordFileWrite(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileWrite(path, success, d)
	}
}

func recordFileDelete(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileDelete(path, success, d)
	}
}

// missingParentDirs returns the list of directories, from the outermost
// missing ancestor down to dir itself, that do not currently exist —
// exactly the directories os.MkdirAll(dir, ...) would create. Returns nil
// if dir already exists. Used by [File.Write]'s CreateDirs to report
// createdDirs (both for a real call and a DryRun preview).
func missingParentDirs(dir string) []string {
	var missing []string
	cur := dir
	for {
		_, statErr := os.Stat(cur)
		if statErr == nil {
			// Exists already (as a directory or otherwise — if it's a
			// non-directory, os.MkdirAll will fail; the caller's real
			// MkdirAll call surfaces that error).
			break
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break // reached filesystem root
		}
		cur = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// ── Template engine ───────────────────────────────────────────────────────────
//
// Subset of api/internal template logic, inlined here to keep format's import
// graph clean (format must not import api/internal).

var fileTemplateVarRe = regexp.MustCompile(`\{([^}]+)\}`)

func buildFromFileTemplate(
	template string,
	vars map[string]string,
	codecs map[string]*codex.Codec[string],
) (string, error) {
	var buildErr error
	result := fileTemplateVarRe.ReplaceAllStringFunc(template, func(placeholder string) string {
		if buildErr != nil {
			return placeholder
		}
		name := strings.TrimSuffix(strings.TrimPrefix(placeholder, "{"), "}")
		value, ok := vars[name]
		if !ok {
			buildErr = MissingFilePathVarError{Name: name}
			return placeholder
		}
		if c, hasCdc := codecs[name]; hasCdc {
			if err := c.Validate(value); err != nil {
				buildErr = FilePathParamError{Name: name, Value: value, Err: err}
				return placeholder
			}
		}
		return value
	})
	if buildErr != nil {
		return "", buildErr
	}
	return result, nil
}

// matchFileTemplate is the inverse of buildFromFileTemplate: it matches a
// concrete path against template, extracting {varName} placeholder values.
// A placeholder captures everything up to the next "/" and may share a
// segment with literal text (e.g. "{date}.json"). Delegates to
// [templatematch.MatchNonWildcard] — the shared, module-internal core also
// used by api/internal and adapters/zeromq, which `ports` cannot import
// directly (api/internal is an api/*-only internal package).
func matchFileTemplate(template, path string) (map[string]string, error) {
	return templatematch.MatchNonWildcard(template, path, func(template, path string) error {
		return FilePathMismatchError{Template: template, Path: path}
	})
}

// ── Typed errors ──────────────────────────────────────────────────────────────

// FilePathParamError is returned by [File.BuildPath] when a {varName} value
// fails its [FilePathParam.Codec] validation.
//
// Use [errors.As] to extract the failing variable name and value:
//
//	var paramErr ports.FilePathParamError
//	if errors.As(err, &paramErr) {
//	    slog.Warn("file path var rejected",
//	        "param", paramErr.Name,
//	        "value", paramErr.Value,
//	        "cause", paramErr.Err,
//	    )
//	}
type FilePathParamError struct {
	Name  string // placeholder name (without braces)
	Value string // the value that failed validation
	Err   error  // underlying constraint or codec error
}

func (e FilePathParamError) Error() string {
	return fmt.Sprintf("file path variable {%s}: invalid value %q: %s", e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FilePathParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FilePathParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}

// FilePathMismatchError is returned by [File.MatchPath] when a concrete
// path does not match the template's structure (wrong number of segments,
// or literal text does not match). Mirrors adapters/mqtt's
// TopicMismatchError exactly (same fields, same rationale) — no wrapped
// cause, this is a self-contained structural mismatch.
type FilePathMismatchError struct {
	Template string
	Path     string
}

func (e FilePathMismatchError) Error() string {
	return fmt.Sprintf("file path %q does not match template %q", e.Path, e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FilePathMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("path", e.Path),
	)
}

// MissingFilePathVarError is returned by [File.BuildPath] when a {varName}
// placeholder has no corresponding entry in the vars map.
//
// Use [errors.As] to extract the missing variable name:
//
//	var missingErr ports.MissingFilePathVarError
//	if errors.As(err, &missingErr) {
//	    slog.Warn("missing file path variable", "param", missingErr.Name)
//	}
type MissingFilePathVarError struct {
	Name string // placeholder name (without braces)
}

func (e MissingFilePathVarError) Error() string {
	return fmt.Sprintf("missing value for file path variable {%s}", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingFilePathVarError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
	)
}

// FileReadError is returned by [File.Read] when [os.ReadFile] fails.
//
// Use [errors.As] to extract the path and underlying OS error.
type FileReadError struct {
	Path string
	Err  error
}

func (e FileReadError) Error() string { return fmt.Sprintf("read file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileReadError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileReadError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileDecodeError is returned by [File.Read] when format decoding or codec
// validation fails after a successful [os.ReadFile].
//
// The wrapped error is typically a [codex.ValidationErrors] — use [errors.As]
// to extract per-field details.
type FileDecodeError struct {
	Path string
	Err  error
}

func (e FileDecodeError) Error() string {
	return fmt.Sprintf("decode file %q: %s", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileDecodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileDecodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileEncodeError is returned by [File.Write] when format encoding or codec
// validation fails before any write to the filesystem.
type FileEncodeError struct {
	Path string
	Err  error
}

func (e FileEncodeError) Error() string {
	return fmt.Sprintf("encode for file %q: %s", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileEncodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileEncodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileWriteError is returned by [File.Write] when [os.WriteFile] fails after
// successful encoding.
type FileWriteError struct {
	Path string
	Err  error
}

func (e FileWriteError) Error() string { return fmt.Sprintf("write file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileWriteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileWriteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileAlreadyExistsError is returned by [File.Write] when [FileOptions.Strict]
// is set and the file already exists (O_CREATE|O_EXCL semantics) — no write
// is performed.
type FileAlreadyExistsError struct {
	Path string
}

func (e FileAlreadyExistsError) Error() string {
	return fmt.Sprintf("file %q already exists (Strict)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileAlreadyExistsError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// FileDeleteError is returned by [File.Delete] when [os.Remove] fails for a
// reason OTHER than "already absent" (e.g. permission denied, path is
// actually a non-empty directory).
type FileDeleteError struct {
	Path string
	Err  error
}

func (e FileDeleteError) Error() string { return fmt.Sprintf("delete file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileDeleteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileDeleteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileNotFoundError is returned by [File.Delete] when [FileOptions.Strict]
// is set and the file did not exist — Delete's default is idempotent
// "ensure absent" success; Strict opts out of that.
type FileNotFoundError struct {
	Path string
}

func (e FileNotFoundError) Error() string {
	return fmt.Sprintf("file %q does not exist (Strict)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// FileWildcardBuildError is returned by [File.BuildPath] when the
// template contains a glob segment ("**", or a segment with '*'/'?'/'[')
// — see [internal/templatematch.IsGlobEnabled]. Building or matching a
// SINGLE concrete path from a template that can match multiple paths is
// undefined; [File.MatchPath] still works against a glob-enabled
// template (matching an externally-discovered path), but BuildPath does
// not, since File has no glob-discovery mechanism of its own — compose
// [Dir.List]'s glob-discovery mode (discover) with [File.Read]/
// [File.Write]/[File.Delete] (per-entry) instead.
type FileWildcardBuildError struct {
	Template string
}

func (e FileWildcardBuildError) Error() string {
	return fmt.Sprintf("file path template %q is glob-enabled: BuildPath has no meaning for a template that can match multiple paths", e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileWildcardBuildError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
	)
}
