package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/jsonschema"
)

// Info holds the server name and version used in spec generation and adapter
// construction.
type Info struct {
	Name    string
	Version string
}

// Builder accumulates registered tools, resources, and prompts.
// Use [NewBuilder] to construct one, then pass it to [Tool.Register],
// [Resource.Register], and [Prompt.Register].
type Builder struct {
	info      Info
	toolNames map[string]struct{}
	resNames  map[string]struct{}
	promNames map[string]struct{}

	toolSpecs []ToolSpec
	resSpecs  []ResourceSpec
	promSpecs []PromptSpec
}

// NewBuilder returns a new Builder with the given server info.
func NewBuilder(info Info) *Builder {
	return &Builder{
		info:      info,
		toolNames: make(map[string]struct{}),
		resNames:  make(map[string]struct{}),
		promNames: make(map[string]struct{}),
	}
}

// Info returns the server info this builder was created with.
func (b *Builder) Info() Info { return b.info }

// MCPSpec returns a static MCP API document listing all registered tools,
// resources, and prompts with their JSON Schemas.
//
// The returned [MCPSpec] is analogous to the OpenAPI spec produced by
// [rest.Server.OpenAPISpec] and the AsyncAPI spec from
// [events.Client.AsyncAPISpec]. It is compatible with the MCP protocol
// tools/list, resources/list, and prompts/list response format.
//
// Marshal to JSON for documentation, testing, or static analysis:
//
//	spec, _ := b.MCPSpec()
//	data, _ := json.MarshalIndent(spec, "", "  ")
func (b *Builder) MCPSpec() (*MCPSpec, error) {
	return &MCPSpec{
		Name:      b.info.Name,
		Version:   b.info.Version,
		Tools:     b.toolSpecs,
		Resources: b.resSpecs,
		Prompts:   b.promSpecs,
	}, nil
}

// MCPSpec is the static MCP API document produced by [Builder.MCPSpec].
// It lists all registered tools, resources, and prompts with their schemas,
// and is compatible with the MCP protocol's list responses.
type MCPSpec struct {
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	Tools     []ToolSpec     `json:"tools,omitempty"`
	Resources []ResourceSpec `json:"resources,omitempty"`
	Prompts   []PromptSpec   `json:"prompts,omitempty"`
}

// ToolSpec is the spec entry for a single tool in [MCPSpec].
type ToolSpec struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// ResourceSpec is the spec entry for a single resource in [MCPSpec].
type ResourceSpec struct {
	URITemplate string   `json:"uriTemplate"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	MimeType    string   `json:"mimeType,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// PromptSpec is the spec entry for a single prompt in [MCPSpec].
type PromptSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Args        []PromptArgSpec `json:"arguments,omitempty"`
}

// PromptArgSpec describes one argument of a prompt in [MCPSpec].
type PromptArgSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ---------------------------------------------------------------------------
// Tool
// ---------------------------------------------------------------------------

// ToolMeta holds tool-level documentation metadata.
// Pass it directly to [NewTool] as a variadic option.
//
// ToolMeta implements [ToolOpt].
type ToolMeta struct {
	// Description is a human-readable description of what the tool does.
	// Shown to LLM clients to help them decide when to call the tool.
	Description string
	// Tags are arbitrary labels attached to the tool for categorisation.
	Tags []string
}

func (m ToolMeta) applyTool(tb *toolBuilder) {
	tb.meta = m
}

// ToolOpt is the sealed interface for [NewTool] options.
//
// The following types implement ToolOpt:
//   - [ToolMeta] — tool-level documentation (description, tags)
//   - [ErrorPattern] — codec-backed typed error result for a matched handler error
type ToolOpt interface{ applyTool(*toolBuilder) }

type toolBuilder struct {
	meta ToolMeta
	// errorPatternRules holds per-tool typed error result declarations from
	// [ErrorPattern] — see [ToolHandle.ErrorResponseFor].
	errorPatternRules []errorPatternRule
}

// Tool[In, Out] is the declarative tool descriptor.
// Construct with [NewTool]; register with [Tool.Register].
// A Tool value can be stored as a package-level variable and registered
// with multiple builders.
type Tool[In, Out any] struct {
	name        string
	inputCodec  codex.Codec[In]
	outputCodec codex.Codec[Out]
	tb          toolBuilder
}

// NewTool returns a declarative tool descriptor that encodes and decodes
// via inputCodec and outputCodec respectively. Call [Tool.Register] to
// obtain a [ToolHandle] for use with an adapter.
//
// name must be non-empty. inputCodec drives input validation and the JSON
// Schema shown to MCP clients. outputCodec validates handler output before
// it is serialised into the tool result.
func NewTool[In, Out any](name string, inputCodec codex.Codec[In], outputCodec codex.Codec[Out], opts ...ToolOpt) Tool[In, Out] {
	var tb toolBuilder
	for _, o := range opts {
		o.applyTool(&tb)
	}
	return Tool[In, Out]{
		name:        name,
		inputCodec:  inputCodec,
		outputCodec: outputCodec,
		tb:          tb,
	}
}

// ToolHandle[In, Out] is returned by [Tool.Register]. It provides:
//   - Typed [ToolHandle.Decode] and [ToolHandle.Encode] helpers backed by the
//     declared codecs.
//   - Rendered JSON Schemas for use by the MCP adapter.
//   - Metadata (Name, Description, Tags) for spec generation.
type ToolHandle[In, Out any] struct {
	// Name is the tool name as registered with the Builder.
	Name string
	// Description is the human-readable tool description.
	Description string
	// Tags are arbitrary labels for this tool.
	Tags []string
	// InputSchema is the JSON Schema derived from the input codec.
	// The MCP adapter sets this on the tool descriptor shown to clients.
	InputSchema json.RawMessage
	// OutputSchema is the JSON Schema derived from the output codec.
	// May be nil when the output codec has no schema (e.g. codex.Any()).
	OutputSchema json.RawMessage

	// Decode deserialises and validates args (the intermediate map[string]any
	// decoded from the MCP call's JSON arguments) into In.
	// All codec Refine constraints run automatically.
	// Errors are wrapped as [ToolInputError].
	Decode func(args any) (In, error)

	// Encode validates out via the output codec and marshals it to JSON bytes
	// for inclusion in the MCP tool result.
	// Errors are wrapped as [ToolOutputError].
	Encode func(out Out) ([]byte, error)

	// errorPatternRules holds per-tool typed error result declarations from
	// [ErrorPattern] — see [ErrorResponseFor].
	errorPatternRules []errorPatternRule
}

// Register validates the tool declaration, renders the input/output JSON
// Schemas, and returns a [ToolHandle]. Returns an error if the tool name
// is empty or already registered with b.
func (t Tool[In, Out]) Register(b *Builder) (*ToolHandle[In, Out], error) {
	if t.name == "" {
		return nil, fmt.Errorf("mcp: tool name must not be empty")
	}
	if _, dup := b.toolNames[t.name]; dup {
		return nil, fmt.Errorf("mcp: tool %q already registered", t.name)
	}

	inputSchema, err := jsonschema.Schema(t.inputCodec.Schema)
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: render input schema: %w", t.name, err)
	}
	outputSchema, err := jsonschema.Schema(t.outputCodec.Schema)
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: render output schema: %w", t.name, err)
	}

	name := t.name
	inputCodec := t.inputCodec
	outputCodec := t.outputCodec
	meta := t.tb.meta

	h := &ToolHandle[In, Out]{
		Name:         name,
		Description:  meta.Description,
		Tags:         meta.Tags,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,

		Decode: func(args any) (In, error) {
			var zero In
			result, err := inputCodec.Decode(args)
			if err != nil {
				return zero, ToolInputError{Name: name, Err: err}
			}
			return result, nil
		},

		Encode: func(out Out) ([]byte, error) {
			intermediate, err := outputCodec.Encode(out)
			if err != nil {
				return nil, ToolOutputError{Name: name, Err: err}
			}
			data, err := json.Marshal(intermediate)
			if err != nil {
				return nil, ToolOutputError{Name: name, Err: fmt.Errorf("json: %w", err)}
			}
			return data, nil
		},

		errorPatternRules: t.tb.errorPatternRules,
	}

	b.toolNames[t.name] = struct{}{}
	b.toolSpecs = append(b.toolSpecs, ToolSpec{
		Name:         name,
		Description:  meta.Description,
		Tags:         meta.Tags,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
	})

	return h, nil
}

// ClientHandle returns a [ToolHandle] without registering with a [Builder].
// No duplicate-name check and no spec registration occur.
//
// Use ClientHandle when only the codec-backed Decode/Encode helpers and
// rendered schemas are needed (no MCP spec document), or when constructing a
// tool handle outside of a [Builder]-managed registration flow.
//
// Mirrors [rest.Route.ClientHandle], [events.Subscriber.Handle]/
// [events.Publisher.Handle] (called with a nil client), and
// [reqreply.Route.ClientHandle].
func (t Tool[In, Out]) ClientHandle() (*ToolHandle[In, Out], error) {
	if t.name == "" {
		return nil, fmt.Errorf("mcp: tool name must not be empty")
	}

	inputSchema, err := jsonschema.Schema(t.inputCodec.Schema)
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: render input schema: %w", t.name, err)
	}
	outputSchema, err := jsonschema.Schema(t.outputCodec.Schema)
	if err != nil {
		return nil, fmt.Errorf("mcp: tool %q: render output schema: %w", t.name, err)
	}

	name := t.name
	inputCodec := t.inputCodec
	outputCodec := t.outputCodec
	meta := t.tb.meta

	return &ToolHandle[In, Out]{
		Name:         name,
		Description:  meta.Description,
		Tags:         meta.Tags,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,

		Decode: func(args any) (In, error) {
			var zero In
			result, err := inputCodec.Decode(args)
			if err != nil {
				return zero, ToolInputError{Name: name, Err: err}
			}
			return result, nil
		},

		Encode: func(out Out) ([]byte, error) {
			intermediate, err := outputCodec.Encode(out)
			if err != nil {
				return nil, ToolOutputError{Name: name, Err: err}
			}
			data, err := json.Marshal(intermediate)
			if err != nil {
				return nil, ToolOutputError{Name: name, Err: fmt.Errorf("json: %w", err)}
			}
			return data, nil
		},

		errorPatternRules: t.tb.errorPatternRules,
	}, nil
}

// ---------------------------------------------------------------------------
// Resource
// ---------------------------------------------------------------------------

// ResourceMeta holds resource-level documentation metadata.
// Pass it directly to [NewResource] as a variadic option.
//
// ResourceMeta implements [ResourceOpt].
type ResourceMeta struct {
	// Name is a short human-readable name for this resource (e.g. "User Profile").
	Name string
	// Description is a human-readable description of what this resource represents.
	Description string
	// MimeType is the MIME type of the resource content (e.g. "application/json").
	MimeType string
	// Tags are arbitrary labels for this resource for categorisation.
	Tags []string
}

func (m ResourceMeta) applyResource(rb *resourceBuilder) {
	rb.meta = m
}

// ResourceOpt is the sealed interface for [NewResource] options.
//
// The following types implement ResourceOpt:
//   - [ResourceMeta] — resource-level metadata (name, description, mimeType)
//   - [URIParam] — a {varName} placeholder's field declaration
type ResourceOpt interface{ applyResource(*resourceBuilder) }

type resourceBuilder struct {
	meta ResourceMeta
	// uriFields holds type-erased codex.FieldCodec[V] values registered
	// via [URIParam] — resolved to []codex.FieldCodec[V] in [NewResource],
	// where V is concrete. Mirrors rest/reqreply's own type-erased
	// mergeFields []any + assertMergeFields pattern.
	uriFields []any
}

// uriParamOpt is the unexported [ResourceOpt] implementation backing
// [URIParam].
type uriParamOpt[V any] struct{ field codex.FieldCodec[V] }

func (o uriParamOpt[V]) applyResource(rb *resourceBuilder) {
	rb.uriFields = append(rb.uriFields, o.field)
}

// URIParam declares a {varName} placeholder in a [NewResource] URI
// template, wrapping any [codex.FieldCodec][V] (typically
// [codex.RequiredField]/[codex.IdentityField]) as a [ResourceOpt] — the
// SAME variadic-fields shape [codex.NewTemplate] itself accepts, just
// usable inline in NewResource's own opts list.
//
//	mcp.NewResource[string, Item]("items://{id}", itemCodec,
//	    mcp.URIParam(codex.IdentityField("id", codex.String().Refine(validate.NonEmptyString))),
//	)
func URIParam[V any](field codex.FieldCodec[V]) ResourceOpt {
	return uriParamOpt[V]{field: field}
}

// Resource[V, T] is the declarative resource descriptor. V is the URI
// template's vars type (e.g. a single-var resource can use V=string
// directly, via an identity [codex.FieldCodec] — see [codex.Template]'s
// own doc for the general pattern); T is the resource's CONTENT type,
// returned by the application handler and encoded via codec. Construct
// with [NewResource] (bare URI template string + [URIParam] opts) or
// [NewResourceFromTemplate] (a pre-built [codex.Template][V]); register
// with [Resource.Register].
type Resource[V, T any] struct {
	template codex.Template[V]
	codec    codex.Codec[T]
	rb       resourceBuilder
}

// NewResource returns a declarative resource descriptor from a bare URI
// template string (may contain "{varName}" placeholders) — the PRIMARY,
// recommended way to declare a resource, matching [rest.NewRoute]/
// [events.NewChannel]/[ports.NewFile]/[ports.NewDir]'s exact "bare string +
// opts" call shape. Declare each "{varName}" via [URIParam]; internally
// builds a [codex.Template][V] via [codex.NewTemplate] with
// [codex.PathStyle]. Call [Resource.Register] to obtain a [ResourceHandle].
//
// PANICS if a "{varName}" in template has no matching [URIParam] declared
// (mirrors [codex.NewTemplate]'s own construction-time panic) or if a
// [URIParam] was declared with the wrong type parameter for V.
//
// codec validates the value returned by the resource handler before it is
// serialised as resource content.
//
//	var itemResource = mcp.NewResource[string, Item]("items://{id}", itemCodec,
//	    mcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
//	    mcp.URIParam(codex.IdentityField("id", codex.String().Refine(validate.NonEmptyString))),
//	)
func NewResource[V, T any](uriTemplate string, codec codex.Codec[T], opts ...ResourceOpt) Resource[V, T] {
	var rb resourceBuilder
	for _, o := range opts {
		o.applyResource(&rb)
	}
	fields := make([]codex.FieldCodec[V], len(rb.uriFields))
	for i, f := range rb.uriFields {
		fc, ok := f.(codex.FieldCodec[V])
		if !ok {
			panic(fmt.Sprintf("api/mcp: NewResource[%T]: URIParam has the wrong type parameter (got %T)", *new(V), f))
		}
		fields[i] = fc
	}
	template := codex.NewTemplate(uriTemplate, codex.PathStyle, fields...)
	return Resource[V, T]{
		template: template,
		codec:    codec,
		rb:       rb,
	}
}

// NewResourceFromTemplate declares a [Resource] from a pre-built
// [codex.Template][V] instead of a bare URI template string + [URIParam]
// opts — mirrors [rest.NewRouteFromPath]/[events.NewChannelFromTopic]/
// [ports.NewFileFromPathTemplate]/[ports.NewDirFromPathTemplate]: reach for
// this when you already have (or want to reuse) a Template[V] value across
// multiple Resource declarations of different T. Decomposes template's own
// declared fields (via [codex.Template.Fields]) back into [URIParam] opts
// and delegates to [NewResource], so the result is IDENTICAL to declaring
// the same template+fields inline.
func NewResourceFromTemplate[V, T any](template codex.Template[V], codec codex.Codec[T], opts ...ResourceOpt) Resource[V, T] {
	fields := template.Fields()
	allOpts := make([]ResourceOpt, 0, len(fields)+len(opts))
	for _, f := range fields {
		allOpts = append(allOpts, URIParam(f))
	}
	allOpts = append(allOpts, opts...)
	return NewResource[V, T](template.String(), codec, allOpts...)
}

// ResourceHandle[V, T] is returned by [Resource.Register]. It provides:
//   - [ResourceHandle.Encode] for codec-validated JSON serialisation.
//   - [ResourceHandle.BuildURI] for constructing concrete URIs from typed vars.
//   - [ResourceHandle.ExtractURIVars] for extracting AND validating vars
//     from a received URI.
type ResourceHandle[V, T any] struct {
	// URITemplate is the registered URI template (may contain {varName} placeholders).
	URITemplate string
	// Name is the short display name for this resource.
	Name string
	// Description is the human-readable resource description.
	Description string
	// MimeType is the content type of this resource.
	MimeType string
	// Tags are arbitrary labels for this resource.
	Tags []string

	// Encode validates v via the codec and marshals it to JSON bytes.
	// Errors are wrapped as [ResourceEncodeError].
	Encode func(v T) ([]byte, error)

	// template is the underlying build+match engine BuildURI/ExtractURIVars
	// delegate to.
	template codex.Template[V]
}

// Register returns a [ResourceHandle] for r. The URI template's own
// {varName} placeholders are validated against r.template's declared
// [codex.FieldCodec] fields at [codex.NewTemplate] CONSTRUCTION time (a
// panic, not a Register-time error — mirrors
// examples/go-edge-models/models/iotedge/usecase/config.go's
// [codex.MustConst]-style "panic at package init" convention), so there is
// no remaining declaration-error path here today. Register still returns an
// error for signature symmetry with [Tool.Register]/[Prompt.Register] —
// future validation can use it without a breaking change.
func (r Resource[V, T]) Register(b *Builder) (*ResourceHandle[V, T], error) {
	uriTemplate := r.template.String()
	codec := r.codec
	meta := r.rb.meta

	h := &ResourceHandle[V, T]{
		URITemplate: uriTemplate,
		Name:        meta.Name,
		Description: meta.Description,
		MimeType:    meta.MimeType,
		Tags:        meta.Tags,
		template:    r.template,

		Encode: func(v T) ([]byte, error) {
			intermediate, err := codec.Encode(v)
			if err != nil {
				return nil, ResourceEncodeError{URI: uriTemplate, Err: err}
			}
			data, err := json.Marshal(intermediate)
			if err != nil {
				return nil, ResourceEncodeError{URI: uriTemplate, Err: fmt.Errorf("json: %w", err)}
			}
			return data, nil
		},
	}

	b.resNames[uriTemplate] = struct{}{}
	b.resSpecs = append(b.resSpecs, ResourceSpec{
		URITemplate: uriTemplate,
		Name:        meta.Name,
		Description: meta.Description,
		MimeType:    meta.MimeType,
		Tags:        meta.Tags,
	})

	return h, nil
}

// BuildURI substitutes vars into [ResourceHandle.URITemplate] via
// [codex.Template.Build], validating each declared field's codec before
// substitution.
//
// Example:
//
//	uri, err := itemResource.BuildURI("abc-123")
//	// uri = "items://abc-123"
func (h *ResourceHandle[V, T]) BuildURI(vars V) (string, error) {
	return h.template.Build(vars)
}

// ExtractURIVars is the inverse of [ResourceHandle.BuildURI]: it matches a
// concrete, received URI against [ResourceHandle.URITemplate] and decodes
// the extracted {varName} placeholder values into a V, ALREADY validated
// against every declared field's codec via [codex.Template.Codec]'s own
// Decode — one call replaces "parse the URI yourself" + "remember to
// validate yourself" (adapters/mcpgo.ResourceHandler calls this
// automatically; see [mcpgo.ResourceVarsHandlerFunc]).
//
// Returns a [codex.TemplateMismatchError] if uri does not match the
// template's structure (wrong number of segments, or a literal segment does
// not match). Returns [codex.ValidationErrors] if an extracted variable
// fails its declared field's codec.
//
// Example:
//
//	id, err := itemResource.ExtractURIVars("items://abc-123")
//	// id == "abc-123"
func (h *ResourceHandle[V, T]) ExtractURIVars(uri string) (V, error) {
	return h.template.Codec().Decode(uri)
}

// ---------------------------------------------------------------------------
// Prompt
// ---------------------------------------------------------------------------

// PromptMeta holds prompt-level documentation metadata.
// Pass it directly to [NewPrompt] as a variadic option.
//
// PromptMeta implements [PromptOpt].
type PromptMeta struct {
	// Description is a human-readable description of what the prompt does.
	Description string
	// Tags are arbitrary labels for this prompt for categorisation.
	Tags []string
}

func (m PromptMeta) applyPrompt(pb *promptBuilder) {
	pb.meta = m
}

// PromptArg describes a named argument for a prompt. Arguments are always
// string-valued in the MCP protocol. Use [PromptArg.WithCodec] to attach
// a string codec for runtime validation.
//
// PromptArg implements [PromptOpt].
type PromptArg struct {
	// Name is the argument name.
	Name string
	// Description is shown in the spec for this argument.
	Description string
	// Required, when true, causes [PromptHandle.ValidateArgs] to return
	// [MissingPromptArgError] when the argument is absent.
	Required bool
	// Codec, when non-nil, validates the argument value at
	// [PromptHandle.ValidateArgs] time.
	// Use [PromptArg.WithCodec] to set it without the address-of pattern.
	Codec *codex.Codec[string]
}

func (a PromptArg) applyPrompt(pb *promptBuilder) {
	pb.args = append(pb.args, a)
}

// WithCodec sets the validation codec and returns the updated PromptArg.
// Use this instead of setting Codec directly:
//
//	mcp.PromptArg{Name: "style", Required: false}.WithCodec(styleCodec)
func (a PromptArg) WithCodec(c codex.Codec[string]) PromptArg {
	a.Codec = &c
	return a
}

// PromptOpt is the sealed interface for [NewPrompt] options.
//
// The following types implement PromptOpt:
//   - [PromptMeta] — prompt-level metadata (description)
//   - [PromptArg] — named argument with optional codec and required flag
type PromptOpt interface{ applyPrompt(*promptBuilder) }

type promptBuilder struct {
	meta PromptMeta
	args []PromptArg
}

// Prompt is the declarative prompt descriptor.
// Construct with [NewPrompt]; register with [Prompt.Register].
// Unlike [Tool] and [Resource], Prompt has no generic type parameter because
// MCP prompt arguments are always string-valued (map[string]string).
type Prompt struct {
	name string
	pb   promptBuilder
}

// NewPrompt returns a declarative prompt descriptor. Call [Prompt.Register]
// to obtain a [PromptHandle].
//
// Use [PromptMeta] to provide documentation and [PromptArg] entries to
// declare the expected arguments with optional validation:
//
//	mcp.NewPrompt("summarize",
//	    mcp.PromptMeta{Description: "Summarize content"},
//	    mcp.PromptArg{Name: "content", Required: true},
//	    mcp.PromptArg{Name: "style"}.WithCodec(styleCodec),
//	)
func NewPrompt(name string, opts ...PromptOpt) Prompt {
	var pb promptBuilder
	for _, o := range opts {
		o.applyPrompt(&pb)
	}
	return Prompt{name: name, pb: pb}
}

// PromptHandle is returned by [Prompt.Register]. It carries the declared
// argument metadata and the [PromptHandle.ValidateArgs] helper.
type PromptHandle struct {
	// Name is the prompt name as registered with the Builder.
	Name string
	// Description is the human-readable prompt description.
	Description string
	// Tags are arbitrary labels for this prompt.
	Tags []string
	// Args is the ordered list of declared prompt arguments.
	// The MCP adapter uses this to populate the prompt's argument list in
	// the spec and to validate incoming arguments.
	Args []PromptArg
}

// Register validates the prompt declaration and returns a [PromptHandle].
// Returns an error if the prompt name is empty or already registered.
func (p Prompt) Register(b *Builder) (*PromptHandle, error) {
	if p.name == "" {
		return nil, fmt.Errorf("mcp: prompt name must not be empty")
	}
	if _, dup := b.promNames[p.name]; dup {
		return nil, fmt.Errorf("mcp: prompt %q already registered", p.name)
	}

	name := p.name
	meta := p.pb.meta
	args := p.pb.args

	// Build spec entries for MCPSpec.
	argSpecs := make([]PromptArgSpec, len(args))
	for i, a := range args {
		argSpecs[i] = PromptArgSpec{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
		}
	}

	h := &PromptHandle{
		Name:        name,
		Description: meta.Description,
		Tags:        meta.Tags,
		Args:        args,
	}

	b.promNames[p.name] = struct{}{}
	b.promSpecs = append(b.promSpecs, PromptSpec{
		Name:        name,
		Description: meta.Description,
		Tags:        meta.Tags,
		Args:        argSpecs,
	})

	return h, nil
}

// ValidateArgs validates the provided args map against the declared [PromptArg]
// definitions: required args must be present, and args with a non-nil Codec are
// validated against it.
//
// A present-but-empty string is treated as a valid value and passed to the codec;
// the codec decides whether "" is acceptable. Only a missing key triggers
// [MissingPromptArgError] for required args.
//
// Returns [MissingPromptArgError] for absent required args;
// [PromptArgError] for codec failures.
func (h *PromptHandle) ValidateArgs(argsMap map[string]string) error {
	for i := range h.Args {
		arg := &h.Args[i]
		val, ok := argsMap[arg.Name]
		if !ok {
			if arg.Required {
				return MissingPromptArgError{Name: arg.Name}
			}
			continue
		}
		// Arg is present (even if empty string) — run codec if set.
		if arg.Codec != nil {
			if err := arg.Codec.Validate(val); err != nil {
				return PromptArgError{Name: arg.Name, Err: err}
			}
		}
	}
	return nil
}
