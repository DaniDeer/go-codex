package format

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/codex"
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
//	format.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date))
func (p FilePathParam) WithCodec(c codex.Codec[string]) FilePathParam {
	p.Codec = &c
	return p
}

func (p FilePathParam) applyFile(fb *fileBuilder) { fb.params = append(fb.params, p) }

// ── FileOptions ───────────────────────────────────────────────────────────────

// FileOptions configures the behaviour of [File.Read], [File.Write], and
// [File.Update].
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
//	var configFile = format.NewFile("config.toml", format.TOML(configCodec))
//
//	// Read
//	cfg, err := configFile.Read(nil, format.FileOptions{Observer: obs})
//
//	// Update (read-modify-write)
//	err = configFile.Update(nil, func(c Config) Config {
// 		c.Port = 9090
// 		return c
//	}, format.FileOptions{Observer: obs})
//
//	// Template path with variable validation
//	var measurementFile = format.NewFile("data/{date}/{sensorID}.json",
//	    format.JSON(measurementCodec),
//	    format.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
//	    format.FilePathParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
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

	format Format[T]
	params []FilePathParam
}

type fileBuilder struct {
	params []FilePathParam
}

// FileOpt is the sealed option interface for [NewFile].
type FileOpt interface{ applyFile(*fileBuilder) }

// NewFile creates a [File] descriptor from a path template, a wire format, and
// optional [FilePathParam] values.
//
// NewFile is infallible — it only captures the spec. Validation of template
// variable names against registered params runs at [File.BuildPath] time.
func NewFile[T any](template string, f Format[T], opts ...FileOpt) File[T] {
	var fb fileBuilder
	for _, opt := range opts {
		opt.applyFile(&fb)
	}
	return File[T]{
		Template: template,
		format:   f,
		params:   fb.params,
	}
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
func (fh File[T]) BuildPath(vars map[string]string) (string, error) {
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
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	path, err := fh.BuildPath(vars)
	if err != nil {
		return zero, err // FilePathParamError or MissingFilePathVarError — no I/O
	}

	start := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		recordFileRead(obs, path, false, time.Since(start))
		return zero, FileReadError{Path: path, Err: err}
	}

	v, err := fh.format.Unmarshal(data)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordFileRead(obs, path, false, time.Since(start))
		return zero, FileDecodeError{Path: path, Err: err}
	}

	recordFileRead(obs, path, true, time.Since(start))
	return v, nil
}

// Write builds the concrete path from vars, encodes v, and writes it to the file.
// The file is created if it does not exist, or truncated and overwritten if it does.
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileEncodeError] — format encode/validation failure
//   - [FileWriteError] — os.WriteFile failure
func (fh File[T]) Write(vars map[string]string, v T, opts FileOptions) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}

	path, err := fh.BuildPath(vars)
	if err != nil {
		return err
	}

	data, err := fh.format.Marshal(v)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordFileWrite(obs, path, false, 0)
		return FileEncodeError{Path: path, Err: err}
	}

	start := time.Now()
	if err := os.WriteFile(path, data, perm); err != nil {
		recordFileWrite(obs, path, false, time.Since(start))
		return FileWriteError{Path: path, Err: err}
	}

	recordFileWrite(obs, path, true, time.Since(start))
	return nil
}

// Update reads the file at the path built from vars, applies fn to the decoded
// value, then writes the result back. It is equivalent to Read followed by Write.
//
// Errors: see [File.Read] and [File.Write].
func (fh File[T]) Update(vars map[string]string, fn func(T) T, opts FileOptions) error {
	v, err := fh.Read(vars, opts)
	if err != nil {
		return err
	}
	return fh.Write(vars, fn(v), opts)
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

// ── Typed errors ──────────────────────────────────────────────────────────────

// FilePathParamError is returned by [File.BuildPath] when a {varName} value
// fails its [FilePathParam.Codec] validation.
//
// Use [errors.As] to extract the failing variable name and value:
//
//	var paramErr format.FilePathParamError
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

// MissingFilePathVarError is returned by [File.BuildPath] when a {varName}
// placeholder has no corresponding entry in the vars map.
//
// Use [errors.As] to extract the missing variable name:
//
//	var missingErr format.MissingFilePathVarError
//	if errors.As(err, &missingErr) {
//	    slog.Warn("missing file path variable", "param", missingErr.Name)
//	}
type MissingFilePathVarError struct {
	Name string // placeholder name (without braces)
}

func (e MissingFilePathVarError) Error() string {
	return fmt.Sprintf("missing value for file path variable {%s}", e.Name)
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

// FileWriteError is returned by [File.Write] when [os.WriteFile] fails after
// successful encoding.
type FileWriteError struct {
	Path string
	Err  error
}

func (e FileWriteError) Error() string { return fmt.Sprintf("write file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileWriteError) Unwrap() error { return e.Err }
