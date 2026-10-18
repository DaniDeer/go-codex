package file

import (
	"errors"
	"fmt"
	"path"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/internal/templatematch"
)

// RouteMiddleware is api/file's public front door onto the shared
// internal/middleware vocabulary — see
// [github.com/DaniDeer/go-codex/api/events.RouteMiddleware]'s identical
// aliasing rationale.
type RouteMiddleware = middleware.RouteMiddleware

// SecurityRequirement maps a scheme name to its required OAuth 2.0
// scopes — see [internal/route.SecurityRequirement] for the full doc
// comment.
type SecurityRequirement = route.SecurityRequirement

// PathParam mirrors [codex.Param]'s shape — the shared, VALIDATE-ONLY
// escape hatch also used by api/rest's PathParam/api/events's TopicParam.
type PathParam struct {
	Name        string
	Description string
	Codec       *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated PathParam.
func (p PathParam) WithCodec(c codex.Codec[string]) PathParam {
	p.Codec = &c
	return p
}

func (p PathParam) applyResource(rb *resourceBuilder) { rb.params = append(rb.params, p) }

func (p PathParam) toParam() codex.Param {
	return codex.Param{Name: p.Name, Description: p.Description, Codec: p.Codec}
}

func toCodexParams(params []PathParam) []codex.Param {
	out := make([]codex.Param, len(params))
	for i, p := range params {
		out[i] = p.toParam()
	}
	return out
}

// MergedPathParam[T] is PathParam's merge-capable sibling — extracted
// path variables are decoded directly into one of T's own fields.
// Mirrors events.NewTopicParam/rest.NewPathParam's existing merge-field
// pattern.
type MergedPathParam[T any] struct {
	codex.MergedParam[T]
}

// NewPathParam declares a path template variable that is BOTH validated
// against codec AND merged into T.
func NewPathParam[T, V any](name string, c codex.Codec[V], get func(T) V, set func(*T, V)) MergedPathParam[T] {
	return MergedPathParam[T]{MergedParam: codex.NewParam(name, c, get, set)}
}

func (p MergedPathParam[T]) applyResource(rb *resourceBuilder) {
	rb.params = append(rb.params, PathParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	rb.mergeFields = append(rb.mergeFields, p.Field)
}

// MetadataParam mirrors [PathParam]'s shape exactly, but validates a
// BACKEND-STORED METADATA value instead of a path variable — api/file's
// analogue of api/rest's HeaderParam/api/events's UserPropertyParam. See
// [MetadataBackend]'s doc comment for the full axis this draws from.
type MetadataParam struct {
	Name        string
	Description string
	Required    bool
	Codec       *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated
// MetadataParam.
func (p MetadataParam) WithCodec(c codex.Codec[string]) MetadataParam {
	p.Codec = &c
	return p
}

func (p MetadataParam) applyResource(rb *resourceBuilder) {
	rb.metadataParams = append(rb.metadataParams, p)
}

// MergedMetadataParam[T] is MetadataParam's merge-capable sibling —
// extracted metadata is decoded directly into one of T's own fields.
// Mirrors [MergedPathParam]'s identical pattern, applied to the
// metadata axis instead of the path-variable axis.
type MergedMetadataParam[T any] struct {
	codex.MergedParam[T]
}

// NewMetadataParam declares a metadata key that is BOTH validated
// against c AND merged into T.
func NewMetadataParam[T, V any](name string, c codex.Codec[V], get func(T) V, set func(*T, V)) MergedMetadataParam[T] {
	return MergedMetadataParam[T]{MergedParam: codex.NewParam(name, c, get, set)}
}

func (p MergedMetadataParam[T]) applyResource(rb *resourceBuilder) {
	rb.metadataParams = append(rb.metadataParams, MetadataParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	rb.metadataMergeFields = append(rb.metadataMergeFields, p.Field)
}

// ResourceOpt is the sealed option interface for [NewResource].
type ResourceOpt interface{ applyResource(*resourceBuilder) }

type resourceBuilder struct {
	params              []PathParam
	mergeFields         []any
	metadataParams      []MetadataParam
	metadataMergeFields []any
}

// Resource[T] declares the file shape once: a path template (mirrors
// api/rest.Path/api/events.Topic) + codec + optional per-variable merge
// fields. Infallible — validation runs at role-attach time.
type Resource[T any] struct {
	template            string
	format              format.Format[T]
	params              []PathParam
	mergeFields         []codex.FieldCodec[T]
	metadataParams      []MetadataParam
	metadataMergeFields []codex.FieldCodec[T]
}

// NewResource creates a Resource descriptor from a path template, a wire
// format, and optional [PathParam]/[MetadataParam] values.
//
// NewResource PANICS if template contains more than one "**" glob
// segment — see [internal/templatematch.ValidateGlobstarCount] —
// mirroring [ports.NewFile]'s identical declare-time structural-template
// safety net.
func NewResource[T any](template string, f format.Format[T], opts ...ResourceOpt) Resource[T] {
	if err := templatematch.ValidateGlobstarCount(template); err != nil {
		panic("api/file: NewResource: " + err.Error())
	}
	var rb resourceBuilder
	for _, opt := range opts {
		opt.applyResource(&rb)
	}
	mergeFields := make([]codex.FieldCodec[T], len(rb.mergeFields))
	for i, mf := range rb.mergeFields {
		fc, ok := mf.(codex.FieldCodec[T])
		if !ok {
			panic(fmt.Sprintf("api/file: NewResource[%T]: merge field %d has the wrong type parameter (got %T)", *new(T), i, mf))
		}
		mergeFields[i] = fc
	}
	metadataMergeFields := make([]codex.FieldCodec[T], len(rb.metadataMergeFields))
	for i, mf := range rb.metadataMergeFields {
		fc, ok := mf.(codex.FieldCodec[T])
		if !ok {
			panic(fmt.Sprintf("api/file: NewResource[%T]: metadata merge field %d has the wrong type parameter (got %T)", *new(T), i, mf))
		}
		metadataMergeFields[i] = fc
	}
	return Resource[T]{
		template:            template,
		format:              f,
		params:              rb.params,
		mergeFields:         mergeFields,
		metadataParams:      rb.metadataParams,
		metadataMergeFields: metadataMergeFields,
	}
}

// Template returns the resource's declared path template.
func (r Resource[T]) Template() string { return r.template }

// MergeFields returns r's declared path-variable merge fields (populated
// via [MergedPathParam]) — mirrors [ports.File.MergeFields]'s identical
// accessor, for a caller composing their own [codex.DecodeVars] call
// (e.g. decoding a [Resource.MatchPath] result into a merge-field-tagged
// struct distinct from the resource's own body T).
func (r Resource[T]) MergeFields() []codex.FieldCodec[T] { return r.mergeFields }

// BuildPath substitutes {varName} placeholders in r's template with vars,
// validating each against its registered [PathParam.Codec]. Returns the
// concrete path on success — a PURE, no-I/O method (no Client/Backend
// needed), mirroring [Dir.BuildPath]'s identical shape.
//
// Returns [FileWildcardBuildError] if the template is glob-enabled (see
// [internal/templatematch.IsGlobEnabled]) — a glob-enabled template does
// not describe a single concrete path; use [MatchPath] (which DOES
// support glob-enabled templates, for validating/extracting vars from an
// already-discovered path) instead.
func (r Resource[T]) BuildPath(vars map[string]string) (string, error) {
	if templatematch.IsGlobEnabled(r.template) {
		return "", FileWildcardBuildError{Template: r.template}
	}
	path, err := codex.BuildFromParams(r.template, toCodexParams(r.params), vars)
	if err != nil {
		return "", convertParamErr(err)
	}
	return path, nil
}

// MatchPath is the inverse of [Resource.BuildPath] — matches a concrete,
// already-discovered path (e.g. from the caller's own
// filepath.WalkDir/[Dir.List]'s glob-discovery mode) against r's own
// template, returning the extracted variable values validated against
// each registered [PathParam.Codec]. A PURE, no-I/O method.
//
// A {varName} placeholder captures everything up to the next "/" and MAY
// share a segment with literal text. Unlike [Resource.BuildPath],
// MatchPath ALSO supports a glob-enabled template — "**"/"*"/"?"/"[...]"
// segments match anonymously (never captured).
//
// Returns [FilePathMismatchError] if path does not match the template's
// structure. Returns [FilePathParamError] if an extracted variable fails
// its registered codec.
func (r Resource[T]) MatchPath(path string) (map[string]string, error) {
	var vars map[string]string
	var err error
	if templatematch.IsGlobEnabled(r.template) {
		vars, err = templatematch.MatchGlob(r.template, path, func(template, path string) error {
			return FilePathMismatchError{Template: template, Path: path}
		})
	} else {
		vars, err = templatematch.MatchNonWildcard(r.template, path, func(template, path string) error {
			return FilePathMismatchError{Template: template, Path: path}
		})
	}
	if err != nil {
		return nil, err
	}
	if err := convertParamErr(codex.ValidateParams(toCodexParams(r.params), vars)); err != nil {
		return nil, err
	}
	return vars, nil
}

// buildPathWithRouting applies routing's prefix (if any — the ONE
// Router-group composition point, see router.go's routingInfo doc
// comment) before building the concrete path.
func (r Resource[T]) buildPathWithRouting(routing routingInfo, vars map[string]string) (string, error) {
	template := routing.resolvedTemplate(r.template)
	path, err := codex.BuildFromParams(template, toCodexParams(r.params), vars)
	if err != nil {
		return "", convertParamErr(err)
	}
	return path, nil
}

// buildParentDirWithRouting builds the PARENT DIRECTORY of r's declared
// path template (stripping the last path segment, which addresses one
// individual resource — e.g. the "{name}.json" segment) — this is the
// directory List/BatchDelete-fallback-enumeration operate over, per the
// roadmap doc's "List operates over the resource's parent directory
// derived from its path template" decision. Only the vars actually
// referenced by the PARENT segment are required — the final segment's
// own var (if any) is deliberately NOT required, since List enumerates
// every value of it.
func (r Resource[T]) buildParentDirWithRouting(routing routingInfo, vars map[string]string) (string, error) {
	template := routing.resolvedTemplate(r.template)
	dir := path.Dir(template)
	built, err := codex.BuildFromParams(dir, toCodexParams(r.params), vars)
	if err != nil {
		return "", convertParamErr(err)
	}
	return built, nil
}

func convertParamErr(err error) error {
	var me codex.MissingParamError
	if errors.As(err, &me) {
		return MissingFilePathVarError{Name: me.Name}
	}
	var pe codex.ParamError
	if errors.As(err, &pe) {
		return FilePathParamError{Name: pe.Name, Value: pe.Value, Err: pe.Err}
	}
	return err
}

// ── Role metadata structs (currently documentation-only fields) ──────────

type Read struct{ Summary, Description string }
type Create struct{ Summary, Description string }
type Patch struct{ Summary, Description string }
type Delete struct{ Summary, Description string }
type List struct{ Summary, Description string }
type Exists struct{ Summary, Description string }
type Copy struct{ Summary, Description string }
type Move struct{ Summary, Description string }
type Mkdir struct{ Summary, Description string }
type BatchDelete struct{ Summary, Description string }

// StreamRead/StreamWrite declare the 2 codec-BYPASSING streaming roles
// (Mechanism 2 — see docs/roadmap/file-api.md's "Streaming/chunked reads
// and writes" section for the full design, including "Why streaming
// bypasses the codec").
type StreamRead struct{ Summary, Description string }
type StreamWrite struct{ Summary, Description string }

// roleCore is the shared, non-exported state every role builder carries —
// NOT embedded (Go's method-locality rule means each role type's
// Use/WithCapabilities/WithSecurity must return ITS OWN type, mirroring
// Subscriber[T]/Publisher[T]'s own per-type method duplication exactly),
// held as a plain field and manipulated via these shared helpers to avoid
// repeating the slice-append logic 10 times.
type roleCore[T any] struct {
	resource     Resource[T]
	security     []SecurityRequirement
	mws          []RouteMiddleware
	capabilities []Capability
	requirements []CapabilityRequirement
	// tags are ACCEPTED and REPORTED (via RouteTags, see router.go) for
	// router.Routable conformance, but api/file has no OpenAPI/AsyncAPI-
	// equivalent spec surface in Phase 1 to consume them — explicitly
	// inert, not silently dropped. See router.go's doc comment.
	tags []string
	// boundMWs holds every REAL, dispatch-time-executed BoundMiddleware
	// attached via a role's own XxxBoundMW(bm any) method — see
	// middleware.go's runBoundMiddleware. Distinct from mws above
	// (.Use()-attached, declare-time-only metadata, never executed).
	boundMWs []roleBoundMW
	// buildErr records a XxxBoundMW(bm any) type-mismatch, checked at the
	// top of .Handle() — mirrors
	// github.com/DaniDeer/go-codex/api/events.Subscriber's identical
	// buildErr field/timing (recorded immediately, surfaced later,
	// since XxxBoundMW's own signature can't return an error).
	buildErr error
	// errorPatterns holds a declared ErrorPatterns[B] (type-erased via
	// errorPatternMatcher, since B varies per attachment) — see
	// error_pattern.go's applyErrorPattern, auto-invoked on every
	// Backend-originated error by all 10 xxxCore functions in
	// dispatch.go. nil (the common case) is zero overhead.
	errorPatterns errorPatternMatcher
}

func (c roleCore[T]) withUse(mws []RouteMiddleware) roleCore[T] {
	c.mws = append(append([]RouteMiddleware{}, c.mws...), mws...)
	return c
}

func (c roleCore[T]) withCapabilities(caps []Capability) roleCore[T] {
	c.capabilities = append(append([]Capability{}, c.capabilities...), caps...)
	return c
}

func (c roleCore[T]) withSecurity(reqs []SecurityRequirement) roleCore[T] {
	c.security = append(append([]SecurityRequirement{}, c.security...), reqs...)
	return c
}

func (c roleCore[T]) withRequirements(reqs []CapabilityRequirement) roleCore[T] {
	c.requirements = append(append([]CapabilityRequirement{}, c.requirements...), reqs...)
	return c
}

// withErrorPatterns attaches patterns (a declared ErrorPatterns[B] for
// whatever B the caller chose) — type-erased via [errorPatternMatcher].
// A mismatch (patterns isn't an ErrorPatterns[B] at all) records
// buildErr, mirroring XxxBoundMW's identical "record, don't panic, first
// error wins" timing.
func (c roleCore[T]) withErrorPatterns(patterns any) roleCore[T] {
	m, ok := patterns.(errorPatternMatcher)
	if !ok {
		if c.buildErr == nil {
			c.buildErr = BackendTypeMismatchError{Expected: "file.ErrorPatterns[B]", Got: typeName(patterns)}
		}
		return c
	}
	c.errorPatterns = m
	return c
}

// checkCoverage runs [CheckCapabilityCoverage] for c's declared
// requirements against c's supplied capabilities — called at Handle
// time, mirroring events.RequireQoS's "fail fast, not silently" contract.
func (c roleCore[T]) checkCoverage() error {
	if len(c.requirements) == 0 {
		return nil
	}
	supplied := make([]any, len(c.capabilities))
	for i, cap_ := range c.capabilities {
		supplied[i] = cap_
	}
	return CheckCapabilityCoverage(c.resource.template, c.requirements, supplied)
}

// ── Reader[T] ──────────────────────────────────────────────────────────

// Reader is returned by [Resource.WithRead].
type Reader[T any] struct {
	core roleCore[T]
	op   Read
}

func (r Resource[T]) WithRead(op Read) Reader[T] {
	return Reader[T]{core: roleCore[T]{resource: r}, op: op}
}

func (r Reader[T]) Use(mws ...RouteMiddleware) Reader[T] {
	r.core = r.core.withUse(mws)
	return r
}
func (r Reader[T]) ErrorPatterns(patterns any) Reader[T] {
	r.core = r.core.withErrorPatterns(patterns)
	return r
}
func (r Reader[T]) WithCapabilities(caps ...Capability) Reader[T] {
	r.core = r.core.withCapabilities(caps)
	return r
}
func (r Reader[T]) WithSecurity(reqs ...SecurityRequirement) Reader[T] {
	r.core = r.core.withSecurity(reqs)
	return r
}
func (r Reader[T]) RequireCapabilities(reqs ...CapabilityRequirement) Reader[T] {
	r.core = r.core.withRequirements(reqs)
	return r
}

// Handle builds the final, immutable *ReadHandle[T]. opts applies
// Router-group composition (see [WithRouter]) — the ONE construction
// point for a Router-grouped path.
func (r Reader[T]) Handle(client *Client, opts ...HandleOpt) (*ReadHandle[T], error) {
	if r.core.buildErr != nil {
		return nil, r.core.buildErr
	}
	if err := r.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &ReadHandle[T]{resource: r.core.resource, core: r.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type ReadHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── Creator[T] ─────────────────────────────────────────────────────────

type Creator[T any] struct {
	core roleCore[T]
	op   Create
}

func (r Resource[T]) WithCreate(op Create) Creator[T] {
	return Creator[T]{core: roleCore[T]{resource: r}, op: op}
}

func (c Creator[T]) Use(mws ...RouteMiddleware) Creator[T] {
	c.core = c.core.withUse(mws)
	return c
}
func (c Creator[T]) ErrorPatterns(patterns any) Creator[T] {
	c.core = c.core.withErrorPatterns(patterns)
	return c
}
func (c Creator[T]) WithCapabilities(caps ...Capability) Creator[T] {
	c.core = c.core.withCapabilities(caps)
	return c
}
func (c Creator[T]) WithSecurity(reqs ...SecurityRequirement) Creator[T] {
	c.core = c.core.withSecurity(reqs)
	return c
}
func (c Creator[T]) RequireCapabilities(reqs ...CapabilityRequirement) Creator[T] {
	c.core = c.core.withRequirements(reqs)
	return c
}

func (c Creator[T]) Handle(client *Client, opts ...HandleOpt) (*CreateHandle[T], error) {
	if c.core.buildErr != nil {
		return nil, c.core.buildErr
	}
	if err := c.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &CreateHandle[T]{resource: c.core.resource, core: c.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type CreateHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── Patcher[T, P] ──────────────────────────────────────────────────────

// Patcher needs its OWN type parameter P (the partial-patch shape) — Go
// requires type parameters on free functions, not methods, so WithPatch
// is a free function taking the Resource, mirroring events/reqreply's
// own "free function, not a method, because of generics" precedent.
type Patcher[T, P any] struct {
	core       roleCore[T]
	op         Patch
	patchCodec codex.Codec[P]
}

func WithPatch[T, P any](r Resource[T], patchCodec codex.Codec[P], op Patch) Patcher[T, P] {
	return Patcher[T, P]{core: roleCore[T]{resource: r}, op: op, patchCodec: patchCodec}
}

func (p Patcher[T, P]) Use(mws ...RouteMiddleware) Patcher[T, P] {
	p.core = p.core.withUse(mws)
	return p
}
func (p Patcher[T, P]) ErrorPatterns(patterns any) Patcher[T, P] {
	p.core = p.core.withErrorPatterns(patterns)
	return p
}
func (p Patcher[T, P]) WithCapabilities(caps ...Capability) Patcher[T, P] {
	p.core = p.core.withCapabilities(caps)
	return p
}
func (p Patcher[T, P]) WithSecurity(reqs ...SecurityRequirement) Patcher[T, P] {
	p.core = p.core.withSecurity(reqs)
	return p
}

func (p Patcher[T, P]) Handle(client *Client, opts ...HandleOpt) (*PatchHandle[T, P], error) {
	if p.core.buildErr != nil {
		return nil, p.core.buildErr
	}
	if err := p.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &PatchHandle[T, P]{resource: p.core.resource, core: p.core, patchCodec: p.patchCodec, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type PatchHandle[T, P any] struct {
	resource   Resource[T]
	core       roleCore[T]
	patchCodec codex.Codec[P]
	client     *Client
	routing    routingInfo
}

// ── Deleter[T] ─────────────────────────────────────────────────────────

type Deleter[T any] struct {
	core roleCore[T]
	op   Delete
}

func (r Resource[T]) WithDelete(op Delete) Deleter[T] {
	return Deleter[T]{core: roleCore[T]{resource: r}, op: op}
}

func (d Deleter[T]) Use(mws ...RouteMiddleware) Deleter[T] {
	d.core = d.core.withUse(mws)
	return d
}
func (d Deleter[T]) ErrorPatterns(patterns any) Deleter[T] {
	d.core = d.core.withErrorPatterns(patterns)
	return d
}
func (d Deleter[T]) WithCapabilities(caps ...Capability) Deleter[T] {
	d.core = d.core.withCapabilities(caps)
	return d
}
func (d Deleter[T]) WithSecurity(reqs ...SecurityRequirement) Deleter[T] {
	d.core = d.core.withSecurity(reqs)
	return d
}
func (d Deleter[T]) RequireCapabilities(reqs ...CapabilityRequirement) Deleter[T] {
	d.core = d.core.withRequirements(reqs)
	return d
}

func (d Deleter[T]) Handle(client *Client, opts ...HandleOpt) (*DeleteHandle[T], error) {
	if d.core.buildErr != nil {
		return nil, d.core.buildErr
	}
	if err := d.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &DeleteHandle[T]{resource: d.core.resource, core: d.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type DeleteHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── Lister[T] ──────────────────────────────────────────────────────────

type Lister[T any] struct {
	core roleCore[T]
	op   List
}

func (r Resource[T]) WithList(op List) Lister[T] {
	return Lister[T]{core: roleCore[T]{resource: r}, op: op}
}

func (l Lister[T]) Use(mws ...RouteMiddleware) Lister[T] {
	l.core = l.core.withUse(mws)
	return l
}
func (l Lister[T]) ErrorPatterns(patterns any) Lister[T] {
	l.core = l.core.withErrorPatterns(patterns)
	return l
}
func (l Lister[T]) WithSecurity(reqs ...SecurityRequirement) Lister[T] {
	l.core = l.core.withSecurity(reqs)
	return l
}

func (l Lister[T]) Handle(client *Client, opts ...HandleOpt) (*ListHandle[T], error) {
	if l.core.buildErr != nil {
		return nil, l.core.buildErr
	}
	h := &ListHandle[T]{resource: l.core.resource, core: l.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type ListHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── Exister[T] ─────────────────────────────────────────────────────────

// WithExists mirrors S3's HeadObject: proves presence/absence and
// returns metadata (size, modtime) WITHOUT downloading/decoding the
// body.
type Exister[T any] struct {
	core roleCore[T]
	op   Exists
}

func (r Resource[T]) WithExists(op Exists) Exister[T] {
	return Exister[T]{core: roleCore[T]{resource: r}, op: op}
}

func (e Exister[T]) Use(mws ...RouteMiddleware) Exister[T] {
	e.core = e.core.withUse(mws)
	return e
}
func (e Exister[T]) ErrorPatterns(patterns any) Exister[T] {
	e.core = e.core.withErrorPatterns(patterns)
	return e
}
func (e Exister[T]) WithSecurity(reqs ...SecurityRequirement) Exister[T] {
	e.core = e.core.withSecurity(reqs)
	return e
}

func (e Exister[T]) Handle(client *Client, opts ...HandleOpt) (*ExistsHandle[T], error) {
	if e.core.buildErr != nil {
		return nil, e.core.buildErr
	}
	h := &ExistsHandle[T]{resource: e.core.resource, core: e.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type ExistsHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── Copier[T] ──────────────────────────────────────────────────────────

// WithCopy mirrors S3's CopyObject: duplicates the resource at a new set
// of path vars without round-tripping bytes through the caller when the
// attached Backend implements the optional CopyingBackend fast path.
type Copier[T any] struct {
	core roleCore[T]
	op   Copy
}

func (r Resource[T]) WithCopy(op Copy) Copier[T] {
	return Copier[T]{core: roleCore[T]{resource: r}, op: op}
}

func (c Copier[T]) Use(mws ...RouteMiddleware) Copier[T] {
	c.core = c.core.withUse(mws)
	return c
}
func (c Copier[T]) ErrorPatterns(patterns any) Copier[T] {
	c.core = c.core.withErrorPatterns(patterns)
	return c
}
func (c Copier[T]) WithCapabilities(caps ...Capability) Copier[T] {
	c.core = c.core.withCapabilities(caps)
	return c
}
func (c Copier[T]) WithSecurity(reqs ...SecurityRequirement) Copier[T] {
	c.core = c.core.withSecurity(reqs)
	return c
}

func (c Copier[T]) Handle(client *Client, opts ...HandleOpt) (*CopyHandle[T], error) {
	if c.core.buildErr != nil {
		return nil, c.core.buildErr
	}
	if err := c.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &CopyHandle[T]{resource: c.core.resource, core: c.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type CopyHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── Mover[T] ───────────────────────────────────────────────────────────

// WithMove mirrors filesystem's atomic os.Rename — S3 has no native move
// for standard buckets (composed from Copy+Delete there instead). Uses
// the optional MovingBackend fast path when the attached Backend
// implements it.
type Mover[T any] struct {
	core roleCore[T]
	op   Move
}

func (r Resource[T]) WithMove(op Move) Mover[T] {
	return Mover[T]{core: roleCore[T]{resource: r}, op: op}
}

func (m Mover[T]) Use(mws ...RouteMiddleware) Mover[T] {
	m.core = m.core.withUse(mws)
	return m
}
func (m Mover[T]) ErrorPatterns(patterns any) Mover[T] {
	m.core = m.core.withErrorPatterns(patterns)
	return m
}
func (m Mover[T]) WithCapabilities(caps ...Capability) Mover[T] {
	m.core = m.core.withCapabilities(caps)
	return m
}
func (m Mover[T]) WithSecurity(reqs ...SecurityRequirement) Mover[T] {
	m.core = m.core.withSecurity(reqs)
	return m
}

func (m Mover[T]) Handle(client *Client, opts ...HandleOpt) (*MoveHandle[T], error) {
	if m.core.buildErr != nil {
		return nil, m.core.buildErr
	}
	if err := m.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &MoveHandle[T]{resource: m.core.resource, core: m.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type MoveHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── MkdirCreator[T] ────────────────────────────────────────────────────

// WithMkdir is the first-class directory-creation operation — the SAME
// Backend.Mkdir call WriteOptions.CreateDirs=true delegates to.
type MkdirCreator[T any] struct {
	core roleCore[T]
	op   Mkdir
}

func (r Resource[T]) WithMkdir(op Mkdir) MkdirCreator[T] {
	return MkdirCreator[T]{core: roleCore[T]{resource: r}, op: op}
}

func (m MkdirCreator[T]) Use(mws ...RouteMiddleware) MkdirCreator[T] {
	m.core = m.core.withUse(mws)
	return m
}
func (m MkdirCreator[T]) ErrorPatterns(patterns any) MkdirCreator[T] {
	m.core = m.core.withErrorPatterns(patterns)
	return m
}
func (m MkdirCreator[T]) WithCapabilities(caps ...Capability) MkdirCreator[T] {
	m.core = m.core.withCapabilities(caps)
	return m
}
func (m MkdirCreator[T]) WithSecurity(reqs ...SecurityRequirement) MkdirCreator[T] {
	m.core = m.core.withSecurity(reqs)
	return m
}

func (m MkdirCreator[T]) Handle(client *Client, opts ...HandleOpt) (*MkdirHandle[T], error) {
	if m.core.buildErr != nil {
		return nil, m.core.buildErr
	}
	if err := m.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &MkdirHandle[T]{resource: m.core.resource, core: m.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type MkdirHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── BatchDeleter[T] ────────────────────────────────────────────────────

// WithBatchDelete mirrors S3's real DeleteObjects operation (delete up
// to 1000 individually-addressed keys in ONE request) — distinct from
// ports.Dir.Delete (which recursively deletes ONE directory's contents
// matched by ONE path template): BatchDeleter[T] takes N INDEPENDENTLY
// addressed path-variable sets, which may not share a common parent
// directory at all.
type BatchDeleter[T any] struct {
	core roleCore[T]
	op   BatchDelete
}

func (r Resource[T]) WithBatchDelete(op BatchDelete) BatchDeleter[T] {
	return BatchDeleter[T]{core: roleCore[T]{resource: r}, op: op}
}

func (b BatchDeleter[T]) Use(mws ...RouteMiddleware) BatchDeleter[T] {
	b.core = b.core.withUse(mws)
	return b
}
func (b BatchDeleter[T]) ErrorPatterns(patterns any) BatchDeleter[T] {
	b.core = b.core.withErrorPatterns(patterns)
	return b
}
func (b BatchDeleter[T]) WithSecurity(reqs ...SecurityRequirement) BatchDeleter[T] {
	b.core = b.core.withSecurity(reqs)
	return b
}

func (b BatchDeleter[T]) Handle(client *Client, opts ...HandleOpt) (*BatchDeleteHandle[T], error) {
	if b.core.buildErr != nil {
		return nil, b.core.buildErr
	}
	h := &BatchDeleteHandle[T]{resource: b.core.resource, core: b.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type BatchDeleteHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── StreamReader[T] ────────────────────────────────────────────────────

// StreamReader[T] is the role value built by Resource[T].WithStreamRead —
// Mechanism 2's codec-bypassing read role (see docs/roadmap/file-api.md's
// "Streaming/chunked reads and writes" section). T still declares path
// templating/Security/Capability at declare time; the ACTUAL bytes
// returned by Client.OpenRead bypass the codec entirely.
type StreamReader[T any] struct {
	core roleCore[T]
	op   StreamRead
}

func (r Resource[T]) WithStreamRead(op StreamRead) StreamReader[T] {
	return StreamReader[T]{core: roleCore[T]{resource: r}, op: op}
}

func (sr StreamReader[T]) Use(mws ...RouteMiddleware) StreamReader[T] {
	sr.core = sr.core.withUse(mws)
	return sr
}
func (sr StreamReader[T]) ErrorPatterns(patterns any) StreamReader[T] {
	sr.core = sr.core.withErrorPatterns(patterns)
	return sr
}
func (sr StreamReader[T]) WithCapabilities(caps ...Capability) StreamReader[T] {
	sr.core = sr.core.withCapabilities(caps)
	return sr
}
func (sr StreamReader[T]) WithSecurity(reqs ...SecurityRequirement) StreamReader[T] {
	sr.core = sr.core.withSecurity(reqs)
	return sr
}
func (sr StreamReader[T]) RequireCapabilities(reqs ...CapabilityRequirement) StreamReader[T] {
	sr.core = sr.core.withRequirements(reqs)
	return sr
}

func (sr StreamReader[T]) Handle(client *Client, opts ...HandleOpt) (*StreamReadHandle[T], error) {
	if sr.core.buildErr != nil {
		return nil, sr.core.buildErr
	}
	if err := sr.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &StreamReadHandle[T]{resource: sr.core.resource, core: sr.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type StreamReadHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}

// ── StreamWriter[T] ────────────────────────────────────────────────────

// StreamWriter[T] is the role value built by Resource[T].WithStreamWrite
// — Mechanism 2's codec-bypassing write role. See StreamReader[T]'s own
// doc comment and docs/roadmap/file-api.md's "Streaming/chunked reads and
// writes" section for the full design.
type StreamWriter[T any] struct {
	core roleCore[T]
	op   StreamWrite
}

func (r Resource[T]) WithStreamWrite(op StreamWrite) StreamWriter[T] {
	return StreamWriter[T]{core: roleCore[T]{resource: r}, op: op}
}

func (sw StreamWriter[T]) Use(mws ...RouteMiddleware) StreamWriter[T] {
	sw.core = sw.core.withUse(mws)
	return sw
}
func (sw StreamWriter[T]) ErrorPatterns(patterns any) StreamWriter[T] {
	sw.core = sw.core.withErrorPatterns(patterns)
	return sw
}
func (sw StreamWriter[T]) WithCapabilities(caps ...Capability) StreamWriter[T] {
	sw.core = sw.core.withCapabilities(caps)
	return sw
}
func (sw StreamWriter[T]) WithSecurity(reqs ...SecurityRequirement) StreamWriter[T] {
	sw.core = sw.core.withSecurity(reqs)
	return sw
}
func (sw StreamWriter[T]) RequireCapabilities(reqs ...CapabilityRequirement) StreamWriter[T] {
	sw.core = sw.core.withRequirements(reqs)
	return sw
}

func (sw StreamWriter[T]) Handle(client *Client, opts ...HandleOpt) (*StreamWriteHandle[T], error) {
	if sw.core.buildErr != nil {
		return nil, sw.core.buildErr
	}
	if err := sw.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &StreamWriteHandle[T]{resource: sw.core.resource, core: sw.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type StreamWriteHandle[T any] struct {
	resource Resource[T]
	core     roleCore[T]
	client   *Client
	routing  routingInfo
}
