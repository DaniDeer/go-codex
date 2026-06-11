// Package mcp provides a transport-agnostic MCP server builder for go-codex.
//
// Define tools, resources, and prompts declaratively with codec-backed types;
// register them with a [Builder] to obtain typed handles. Pass those handles to
// an MCP adapter (e.g. adapters/mcpgo) to wire them to a running server.
//
// This package does not import any MCP SDK — it is framework-agnostic.
// The workflow is the same declare → register → handle pattern as [api/rest]
// and [api/events]:
//
//	// Layer 1: define codecs
//	inputCodec  := codex.Struct[CalcInput](...)
//	outputCodec := codex.Struct[CalcOutput](...)
//
//	// Layer 2: declare tools/resources/prompts as values
//	var calcTool = mcp.NewTool[CalcInput, CalcOutput]("calculate",
//	    inputCodec, outputCodec,
//	    mcp.ToolMeta{Description: "Perform arithmetic"},
//	)
//
//	var itemResource = mcp.NewResource[Item]("items://{id}", itemCodec,
//	    mcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
//	    mcp.ResourceParam{Name: "id"}.WithCodec(uuidCodec),
//	)
//
//	var summaryPrompt = mcp.NewPrompt("summarize",
//	    mcp.PromptMeta{Description: "Summarize content"},
//	    mcp.PromptArg{Name: "content", Required: true},
//	)
//
//	// Register with a builder
//	b := mcp.NewBuilder(mcp.Info{Name: "My Server", Version: "1.0.0"})
//	toolHandle, _     := calcTool.Register(b)
//	resHandle, _      := itemResource.Register(b)
//	promptHandle, _   := summaryPrompt.Register(b)
//
//	// Spec generation (analogous to OpenAPISpec / AsyncAPISpec)
//	spec, _ := b.MCPSpec()
//
//	// Adapter layer (adapters/mcpgo):
//	// mcpgo.RegisterTool(s, toolHandle, fn, opts)
//	// mcpgo.RegisterResource(s, resHandle, fn, opts)
//	// mcpgo.RegisterPrompt(s, promptHandle, fn, opts)
package mcp

import (
	"encoding/json"
	"fmt"
	"github.com/DaniDeer/go-codex/api/internal"
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
// [rest.Builder.OpenAPISpec] and the AsyncAPI spec from
// [events.Builder.AsyncAPISpec]. It is compatible with the MCP protocol
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
type ToolOpt interface{ applyTool(*toolBuilder) }

type toolBuilder struct {
	meta ToolMeta
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

// ResourceParam describes a {varName} placeholder in a resource URI template.
// It optionally carries a codec for runtime validation of the variable value.
//
// ResourceParam implements [ResourceOpt].
type ResourceParam struct {
	// Name is the variable name (without braces) as it appears in the URI template.
	Name string
	// Description is shown in the spec for this parameter.
	Description string
	// Codec validates URI parameter values at [ResourceHandle.ValidateURIVars]
	// and [ResourceHandle.BuildURI] time. Nil means no runtime validation.
	Codec *codex.Codec[string]
}

func (p ResourceParam) applyResource(rb *resourceBuilder) {
	rb.uriParams = append(rb.uriParams, p)
}

// WithCodec sets the validation codec and returns the updated ResourceParam.
// Use this instead of setting Codec directly to avoid the address-of pattern:
//
//	mcp.ResourceParam{Name: "id"}.WithCodec(uuidCodec)
func (p ResourceParam) WithCodec(c codex.Codec[string]) ResourceParam {
	p.Codec = &c
	return p
}

// ResourceOpt is the sealed interface for [NewResource] options.
//
// The following types implement ResourceOpt:
//   - [ResourceMeta] — resource-level metadata (name, description, mimeType)
//   - [ResourceParam] — URI template variable with optional codec
type ResourceOpt interface{ applyResource(*resourceBuilder) }

type resourceBuilder struct {
	meta      ResourceMeta
	uriParams []ResourceParam
}

// Resource[T] is the declarative resource descriptor.
// Construct with [NewResource]; register with [Resource.Register].
type Resource[T any] struct {
	uriTemplate string
	codec       codex.Codec[T]
	rb          resourceBuilder
}

// NewResource returns a declarative resource descriptor. uriTemplate may
// contain {varName} placeholders (e.g. "items://{id}"). Call [Resource.Register]
// to obtain a [ResourceHandle].
//
// codec validates the value returned by the resource handler before it is
// serialised as resource content.
func NewResource[T any](uriTemplate string, codec codex.Codec[T], opts ...ResourceOpt) Resource[T] {
	var rb resourceBuilder
	for _, o := range opts {
		o.applyResource(&rb)
	}
	return Resource[T]{
		uriTemplate: uriTemplate,
		codec:       codec,
		rb:          rb,
	}
}

// ResourceHandle[T] is returned by [Resource.Register]. It provides:
//   - [ResourceHandle.Encode] for codec-validated JSON serialisation.
//   - [ResourceHandle.BuildURI] for constructing concrete URIs from template vars.
//   - [ResourceHandle.ValidateURIVars] for validating extracted vars.
type ResourceHandle[T any] struct {
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

	// BuildURI substitutes {varName} placeholders in URITemplate with the
	// values from vars, validating each against its registered codec.
	// Returns [MissingResourceURIVarError] for absent vars;
	// [ResourceURIVarError] for codec failures.
	BuildURI func(vars map[string]string) (string, error)

	// ValidateURIVars validates extracted URI variable values against registered
	// [ResourceParam] codecs. Returns [ResourceURIVarError] on the first failure.
	// Variables without a registered codec are skipped.
	ValidateURIVars func(vars map[string]string) error
}

// Register validates the resource declaration and returns a [ResourceHandle].
// Returns an error if the URI template is empty or unknown {varName}
// placeholders are found in uriParams that do not appear in the template.
func (r Resource[T]) Register(b *Builder) (*ResourceHandle[T], error) {
	if r.uriTemplate == "" {
		return nil, fmt.Errorf("mcp: resource URI template must not be empty")
	}

	// Validate that all declared ResourceParams appear in the template.
	templateVars := internal.ParseTemplateVars(r.uriTemplate)
	for _, p := range r.rb.uriParams {
		if !templateVars[p.Name] {
			return nil, fmt.Errorf("mcp: resource %q: URI param %q not found in template %q",
				r.rb.meta.Name, p.Name, r.uriTemplate)
		}
	}

	uriTemplate := r.uriTemplate
	codec := r.codec
	meta := r.rb.meta
	uriParams := r.rb.uriParams

	// Build codec lookup for BuildURI/ValidateURIVars.
	codecMap := make(map[string]*codex.Codec[string], len(uriParams))
	for i := range uriParams {
		if uriParams[i].Codec != nil {
			codecMap[uriParams[i].Name] = uriParams[i].Codec
		}
	}

	h := &ResourceHandle[T]{
		URITemplate: uriTemplate,
		Name:        meta.Name,
		Description: meta.Description,
		MimeType:    meta.MimeType,
		Tags:        meta.Tags,

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

		BuildURI: func(vars map[string]string) (string, error) {
			return internal.BuildFromTemplate(uriTemplate, vars, codecMap,
				func(name string) error { return MissingResourceURIVarError{Name: name} },
				func(name, value string, err error) error {
					return ResourceURIVarError{Name: name, Value: value, Err: err}
				},
			)
		},

		ValidateURIVars: func(vars map[string]string) error {
			for i := range uriParams {
				p := &uriParams[i]
				if p.Codec == nil {
					continue
				}
				val, ok := vars[p.Name]
				if !ok {
					return MissingResourceURIVarError{Name: p.Name}
				}
				if err := p.Codec.Validate(val); err != nil {
					return ResourceURIVarError{Name: p.Name, Value: val, Err: err}
				}
			}
			return nil
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

	// ValidateArgs validates the provided args map against the declared
	// [PromptArg] definitions: required args must be present, and args with
	// a non-nil Codec are validated against it.
	// Returns [MissingPromptArgError] for absent required args;
	// [PromptArgError] for codec failures.
	ValidateArgs func(args map[string]string) error
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

		ValidateArgs: func(argsMap map[string]string) error {
			for i := range args {
				arg := &args[i]
				val, ok := argsMap[arg.Name]
				if !ok || val == "" {
					if arg.Required {
						return MissingPromptArgError{Name: arg.Name}
					}
					continue
				}
				if arg.Codec != nil {
					if err := arg.Codec.Validate(val); err != nil {
						return PromptArgError{Name: arg.Name, Err: err}
					}
				}
			}
			return nil
		},
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
