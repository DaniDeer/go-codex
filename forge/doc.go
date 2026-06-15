// Package forge provides governed, self-documenting KPI computation functions.
//
// A [Function][In, Out] carries its input and output codecs, a SHA-256 contract
// hash, version string, and optional governance metadata (author, approver,
// approval date) — all in one value. The contract hash changes whenever the
// codec or the compute function changes, making silent drift impossible.
//
// # Defining a function
//
//	var oeeFunction = forge.NewFunction[OEEInput, OEEResult](
//	    "oee", "1.0.0",
//	    oeeInputCodec, oeeResultCodec,
//	    func(ctx context.Context, in OEEInput) (OEEResult, error) {
//	        return OEEResult{OEE: in.Availability * in.Performance * in.Quality}, nil
//	    },
//	    forge.FunctionMeta{
//	        Description: "Overall Equipment Effectiveness (ISO 22400-2).",
//	        Author:      "engineering@example.com",
//	        ApprovedBy:  "quality-board",
//	        ApprovedAt:  "2024-01-15",
//	    },
//	)
//
// # Validation sequence
//
// When [Function.Apply] is called:
//
//  1. Input codec decodes and validates → [InputError] on failure
//  2. Optional cross-input refinement runs → [RefinementError] on failure
//  3. User function executes → [ApplyError] on failure
//  4. Output codec validates → [OutputError] on failure
//
// # Composing functions
//
// [Compose] chains two functions type-safely:
//
//	var pipeline = forge.Compose(rawDataFn, oeeFn)
//	// type-checked: Out of rawDataFn must match In of oeeFn
//
// # Registering in a pipeline
//
//	registry := forge.NewRegistry("OEE Pipeline", "1.0.0").
//	    WithAuthor("engineering@example.com").
//	    WithApproval("quality-board", "2024-01-15").
//	    WithObserver(obs)
//
//	registry.Register(oeeFunction)
//	spec := registry.PipelineSpec()
//	// render with render/pipeline to produce a YAML governance document
//
// # Collection operations
//
// Lift a function over a slice or map:
//
//	batchOEE  := forge.Map(oeeFunction)    // []OEEInput → []OEEResult
//	alerts    := forge.Filter(alertFn)     // []Reading → []Reading
//	total     := forge.Reduce(sumFn)       // ([]Reading, float64) → float64
//
// # Binary data in forge functions
//
// [NewFunction] accepts any codec type, including [codex.Bytes] for raw binary data
// (images, documents, sensor captures). Binary functions work exactly like numeric or
// struct functions — the codec validates on both input and output via Refine constraints:
//
//	pngCodec := codex.Bytes().
//	    Refine(validate.MaxBytes(5 * 1024 * 1024)).
//	    Refine(validate.PNG).
//	    WithTitle("rawImage")
//
//	// Function that validates and processes a raw PNG
//	resizeImage := forge.NewFunction("resizeImage", "1.0.0",
//	    pngCodec,
//	    pngCodec.WithTitle("resizedImage"),
//	    func(raw []byte) ([]byte, error) {
//	        return resizePNG(raw, 128, 128)
//	    },
//	)
//
// Port names are inferred from [codex.Codec.WithTitle]; the pipeline YAML emits
// schema {type: string, format: binary} for binary ports.
//
// # MeasuredCodec with binary values
//
// [MeasuredCodec] wraps any codec, including binary:
//
//	measured := forge.MeasuredCodec(codex.Bytes().Refine(validate.PNG))
//
// However, if the resulting [Measured][[]byte] value is later serialised via
// [format.JSON] (REST response, MQTT JSON body), Go's JSON encoder base64-encodes
// []byte values automatically — the consumer receives base64, not raw bytes.
//
// Choose the value codec based on the downstream serialisation:
//
//   - [codex.Bytes] (raw) — use when Measured is published via [format.Binary] or
//     consumed only within the forge computation layer (no JSON serialisation).
//   - [codex.Base64] — use when Measured is published via JSON (REST, MQTT JSON),
//     making the base64 encoding explicit and round-trip correct.
//
// # Further reading
//
//   - [render/pipeline] — renders a [PipelineSpec] to YAML
//   - [codex] — codec primitives used for input/output contracts
//   - [validate.PNG], [validate.JPEG], [validate.PDF] — built-in binary format constraints
package forge
