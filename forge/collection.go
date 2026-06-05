package forge

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// containerSlice returns a Codec[[]T] that validates only the outer container type —
// it does NOT validate individual elements. Used as the input codec for Map, Filter,
// and Reduce so that per-element errors are reported as CollectionElementError (with
// an explicit element index) rather than as InputError wrapping codex.ElementError.
//
// The schema is identical to codex.SliceOf(elem).Schema so spec generation is correct.
func containerSlice[T any](elem codex.Codec[T]) codex.Codec[[]T] {
	itemSchema := elem.Schema
	return codex.Codec[[]T]{
		Schema: schema.Schema{Type: "array", Items: &itemSchema},
		Encode: func(vs []T) (any, error) {
			out := make([]any, len(vs))
			for i, v := range vs {
				out[i] = v
			}
			return out, nil
		},
		Decode: func(v any) ([]T, error) {
			switch raw := v.(type) {
			case []T:
				return raw, nil
			case []any:
				out := make([]T, len(raw))
				for i, item := range raw {
					t, ok := item.(T)
					if !ok {
						var zero T
						return nil, fmt.Errorf("element %d: expected %T, got %T", i, zero, item)
					}
					out[i] = t
				}
				return out, nil
			default:
				return nil, codex.TypeMismatchError{Expected: "array", Got: fmt.Sprintf("%T", v)}
			}
		},
	}
}

// containerMap returns a Codec[map[string]V] that validates only the outer container —
// it does NOT validate individual values. Used as the input codec for MapValues so that
// per-key errors are reported as CollectionKeyError rather than InputError wrapping
// codex.KeyError.
func containerMap[V any](value codex.Codec[V]) codex.Codec[map[string]V] {
	valueSchema := value.Schema
	return codex.Codec[map[string]V]{
		Schema: schema.Schema{
			Type:                       "object",
			AdditionalPropertiesSchema: &valueSchema,
		},
		Encode: func(m map[string]V) (any, error) {
			out := make(map[string]any, len(m))
			for k, v := range m {
				out[k] = v
			}
			return out, nil
		},
		Decode: func(v any) (map[string]V, error) {
			switch raw := v.(type) {
			case map[string]V:
				return raw, nil
			case map[string]any:
				out := make(map[string]V, len(raw))
				for k, item := range raw {
					typed, ok := item.(V)
					if !ok {
						var zero V
						return nil, fmt.Errorf("key %q: expected %T, got %T", k, zero, item)
					}
					out[k] = typed
				}
				return out, nil
			default:
				return nil, codex.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
		},
	}
}

// containerMapK returns a Codec[map[K]V] that validates every map key using keyCodec
// before returning the decoded map. Values are NOT validated — per-value errors are
// reported as CollectionKeyError by MapValuesK, not as InputError wrapping codex.KeyError.
//
// An invalid key causes the codec to return codex.KeyError immediately (fail-fast),
// which Apply wraps as InputError. All keys are valid or none pass.
func containerMapK[K comparable, V any](keyCodec codex.Codec[K], value codex.Codec[V]) codex.Codec[map[K]V] {
	keySchema := keyCodec.Schema
	valueSchema := value.Schema
	return codex.Codec[map[K]V]{
		Schema: schema.Schema{
			Type:                       "object",
			PropertyNames:              &keySchema,
			AdditionalPropertiesSchema: &valueSchema,
		},
		Encode: func(m map[K]V) (any, error) {
			out := make(map[string]any, len(m))
			for k, v := range m {
				rawKey, err := keyCodec.Encode(k)
				if err != nil {
					return nil, codex.KeyError{Key: fmt.Sprintf("%v", k), Err: err}
				}
				strKey, ok := rawKey.(string)
				if !ok {
					return nil, codex.KeyError{
						Key: fmt.Sprintf("%v", k),
						Err: fmt.Errorf("key codec must encode to string, got %T", rawKey),
					}
				}
				out[strKey] = v
			}
			return out, nil
		},
		Decode: func(v any) (map[K]V, error) {
			switch raw := v.(type) {
			case map[K]V:
				return raw, nil
			case map[string]any:
				out := make(map[K]V, len(raw))
				for strKey, item := range raw {
					k, err := keyCodec.Decode(strKey)
					if err != nil {
						return nil, codex.KeyError{Key: strKey, Err: err}
					}
					typed, ok := item.(V)
					if !ok {
						var zero V
						return nil, codex.KeyError{
							Key: strKey,
							Err: fmt.Errorf("expected %T, got %T", zero, item),
						}
					}
					out[k] = typed
				}
				return out, nil
			default:
				return nil, codex.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
		},
	}
}

// Map lifts fn to operate element-wise over a slice: []In → []Out.
//
// Each element is validated and processed by fn. On the first element failure
// (input validation, refinement, compute, or output validation), Map returns a
// CollectionElementError carrying the element index and the underlying error.
//
// The resulting Function carries Kind="map" and Wraps=fn.Spec.Name in its
// FunctionSpec, making the relationship visible in the pipeline YAML spec.
//
// The full governance options (WithDescription, WithAuthor, WithApproval,
// WithRefinement) work on the slice-level function. WithRefinement receives the
// whole []In slice — use it for collection-level constraints (e.g. minimum count).
//
// Panics if name or version is empty — these are programming errors.
//
//	batchCalc := forge.Map("batchCalc", "1.0.0", oeeCalc,
//	    forge.WithDescription("Applies oeeCalc to a batch of measurements."),
//	    forge.WithRefinement(func(in []OEEIn) error {
//	        if len(in) == 0 {
//	            return fmt.Errorf("batch must not be empty")
//	        }
//	        return nil
//	    }),
//	)
func Map[In, Out any](
	name, version string,
	fn *Function[In, Out],
	opts ...FunctionOpt,
) *Function[[]In, []Out] {
	if name == "" {
		panic("forge.Map: name must not be empty")
	}
	if version == "" {
		panic("forge.Map: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inputCodec := containerSlice(fn.inputCodec)
	outputCodec := codex.SliceOf(fn.output)
	inputs := inputSpecs(fn.Spec.Inputs[0].Name, inputCodec.Schema)
	out := PortSpec{Name: fn.Spec.Output.Name, Schema: outputCodec.Schema}
	spec := FunctionSpec{
		Name:        name,
		Version:     version,
		Hash:        computeHash(name, version, inputs, out),
		Kind:        FunctionKindMap,
		Wraps:       fn.Spec.Name,
		Description: cfg.description,
		Author:      cfg.author,
		ApprovedBy:  cfg.approvedBy,
		ApprovedAt:  cfg.approvedAt,
		Inputs:      inputs,
		Output:      out,
	}
	fnName := name
	apply := func(items []In) ([]Out, error) {
		result := make([]Out, len(items))
		for i, item := range items {
			v, err := fn.Apply(item)
			if err != nil {
				return nil, CollectionElementError{Function: fnName, Index: i, Err: err}
			}
			result[i] = v
		}
		return result, nil
	}
	var refinement func([]In) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(v []In) error { return r(v) }
	}
	return &Function[[]In, []Out]{
		Spec:       spec,
		inputCodec: inputCodec,
		output:     outputCodec,
		apply:      apply,
		refinement: refinement,
	}
}

// Filter returns a Function[[]T, []T] that keeps only elements satisfying pred.
//
// Each element is validated by elemCodec before pred is called. On the first
// element validation failure, Filter returns a CollectionElementError.
// Elements for which pred returns false are silently dropped (no error).
//
// The FunctionSpec carries Kind="filter".
// Panics if name or version is empty — these are programming errors.
//
//	warmFilter := forge.Filter("warmFilter", "1.0.0", rawCodec,
//	    func(r RawReading) bool { return r.WarmUp },
//	)
func Filter[T any](
	name, version string,
	elemCodec codex.Codec[T],
	pred func(T) bool,
	opts ...FunctionOpt,
) *Function[[]T, []T] {
	if name == "" {
		panic("forge.Filter: name must not be empty")
	}
	if version == "" {
		panic("forge.Filter: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inputCodec := containerSlice(elemCodec)
	outputCodec := codex.SliceOf(elemCodec)
	elemName := elemCodec.Schema.Title
	if elemName == "" {
		elemName = "elements"
	}
	inputs := inputSpecs(elemName, inputCodec.Schema)
	out := PortSpec{Name: elemName, Schema: outputCodec.Schema}
	spec := FunctionSpec{
		Name:        name,
		Version:     version,
		Hash:        computeHash(name, version, inputs, out),
		Kind:        FunctionKindFilter,
		Description: cfg.description,
		Author:      cfg.author,
		ApprovedBy:  cfg.approvedBy,
		ApprovedAt:  cfg.approvedAt,
		Inputs:      inputs,
		Output:      out,
	}
	fnName := name
	apply := func(items []T) ([]T, error) {
		result := make([]T, 0, len(items))
		for i, item := range items {
			if err := elemCodec.Validate(item); err != nil {
				return nil, CollectionElementError{Function: fnName, Index: i, Err: err}
			}
			if pred(item) {
				result = append(result, item)
			}
		}
		return result, nil
	}
	var refinement func([]T) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(v []T) error { return r(v) }
	}
	return &Function[[]T, []T]{
		Spec:       spec,
		inputCodec: inputCodec,
		output:     outputCodec,
		apply:      apply,
		refinement: refinement,
	}
}

// Reduce returns a Function[[]T, Acc] that folds the slice to a single accumulator.
//
// Each element is validated by elemCodec before the step function is called.
// On the first element validation failure, Reduce returns a CollectionElementError.
// The final accumulator is validated by accCodec before being returned.
//
// The FunctionSpec carries Kind="reduce".
// Panics if name or version is empty — these are programming errors.
//
//	summarise := forge.Reduce("summarise", "1.0.0",
//	    celsiusCodec, summaryCodec,
//	    BatchSummary{},
//	    func(acc BatchSummary, c Celsius) BatchSummary { ... },
//	)
func Reduce[T, Acc any](
	name, version string,
	elemCodec codex.Codec[T],
	accCodec codex.Codec[Acc],
	init Acc,
	step func(Acc, T) Acc,
	opts ...FunctionOpt,
) *Function[[]T, Acc] {
	if name == "" {
		panic("forge.Reduce: name must not be empty")
	}
	if version == "" {
		panic("forge.Reduce: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inputCodec := containerSlice(elemCodec)
	elemName2 := elemCodec.Schema.Title
	if elemName2 == "" {
		elemName2 = "elements"
	}
	accName := accCodec.Schema.Title
	if accName == "" {
		accName = "result"
	}
	inputs := inputSpecs(elemName2, inputCodec.Schema)
	out := PortSpec{Name: accName, Schema: accCodec.Schema}
	spec := FunctionSpec{
		Name:        name,
		Version:     version,
		Hash:        computeHash(name, version, inputs, out),
		Kind:        FunctionKindReduce,
		Description: cfg.description,
		Author:      cfg.author,
		ApprovedBy:  cfg.approvedBy,
		ApprovedAt:  cfg.approvedAt,
		Inputs:      inputs,
		Output:      out,
	}
	fnName := name
	apply := func(items []T) (Acc, error) {
		acc := init
		for i, item := range items {
			if err := elemCodec.Validate(item); err != nil {
				var zero Acc
				return zero, CollectionElementError{Function: fnName, Index: i, Err: err}
			}
			acc = step(acc, item)
		}
		return acc, nil
	}
	var refinement func([]T) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(v []T) error { return r(v) }
	}
	return &Function[[]T, Acc]{
		Spec:       spec,
		inputCodec: inputCodec,
		output:     accCodec,
		apply:      apply,
		refinement: refinement,
	}
}

// MapValues lifts fn to operate over all values of a map[string]In → map[string]Out.
//
// Each value is processed by fn. On the first key failure (input validation,
// refinement, compute, or output validation), MapValues returns a CollectionKeyError
// carrying the key and the underlying error. Iteration order is not guaranteed.
//
// The FunctionSpec carries Kind="mapValues" and Wraps=fn.Spec.Name.
//
// Map keys are not validated by MapValues. To enforce a key format, use MapValuesK.
// Panics if name or version is empty — these are programming errors.
//
//	batchPerSensor := forge.MapValues("batchPerSensor", "1.0.0", summariseCalc)
func MapValues[In, Out any](
	name, version string,
	fn *Function[In, Out],
	opts ...FunctionOpt,
) *Function[map[string]In, map[string]Out] {
	if name == "" {
		panic("forge.MapValues: name must not be empty")
	}
	if version == "" {
		panic("forge.MapValues: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inputCodec := containerMap(fn.inputCodec)
	outputCodec := codex.StringMap(fn.output)
	inputs := inputSpecs(fn.Spec.Inputs[0].Name, inputCodec.Schema)
	out := PortSpec{Name: fn.Spec.Output.Name, Schema: outputCodec.Schema}
	spec := FunctionSpec{
		Name:        name,
		Version:     version,
		Hash:        computeHash(name, version, inputs, out),
		Kind:        FunctionKindMapValues,
		Wraps:       fn.Spec.Name,
		Description: cfg.description,
		Author:      cfg.author,
		ApprovedBy:  cfg.approvedBy,
		ApprovedAt:  cfg.approvedAt,
		Inputs:      inputs,
		Output:      out,
	}
	fnName := name
	apply := func(items map[string]In) (map[string]Out, error) {
		result := make(map[string]Out, len(items))
		for k, item := range items {
			v, err := fn.Apply(item)
			if err != nil {
				return nil, CollectionKeyError{Function: fnName, Key: k, Err: err}
			}
			result[k] = v
		}
		return result, nil
	}
	var refinement func(map[string]In) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(v map[string]In) error { return r(v) }
	}
	return &Function[map[string]In, map[string]Out]{
		Spec:       spec,
		inputCodec: inputCodec,
		output:     outputCodec,
		apply:      apply,
		refinement: refinement,
	}
}

// MapValuesK lifts fn to operate over all values of a map[K]In → map[K]Out, with
// key validation enforced by keyCodec before any element is processed.
//
// Key validation is fail-fast: the first key that fails keyCodec causes Apply to
// return InputError → KeyError → ConstraintError. No values are processed until all
// keys pass. This mirrors the behaviour of codex.Map[K, V] used as an input codec.
//
// Per-value failures (input, refinement, compute, or output within fn) are returned
// as CollectionKeyError, same as MapValues.
//
// K must satisfy comparable and keyCodec must encode to string (JSON/YAML requirement).
// Use codex.String().Refine(...) for pattern or enum constraints on string keys.
//
// The FunctionSpec carries Kind="mapValues" and Wraps=fn.Spec.Name. The input schema
// gains a "propertyNames" entry from keyCodec.Schema.
//
// Panics if name or version is empty — these are programming errors.
//
//	perSensor := forge.MapValuesK("perSensor", "1.0.0",
//	    sensorIDCodec,  // enforces ^[a-z]+-\d+$ on every key
//	    singleSensorPipeline,
//	    forge.WithDescription("Applies the sensor pipeline to every validated sensor key."),
//	)
func MapValuesK[K comparable, In, Out any](
	name, version string,
	keyCodec codex.Codec[K],
	fn *Function[In, Out],
	opts ...FunctionOpt,
) *Function[map[K]In, map[K]Out] {
	if name == "" {
		panic("forge.MapValuesK: name must not be empty")
	}
	if version == "" {
		panic("forge.MapValuesK: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inputCodec := containerMapK(keyCodec, fn.inputCodec)
	outputCodec := codex.Map[K, Out](keyCodec, fn.output)
	inputs := inputSpecs(fn.Spec.Inputs[0].Name, inputCodec.Schema)
	out := PortSpec{Name: fn.Spec.Output.Name, Schema: outputCodec.Schema}
	spec := FunctionSpec{
		Name:        name,
		Version:     version,
		Hash:        computeHash(name, version, inputs, out),
		Kind:        FunctionKindMapValues,
		Wraps:       fn.Spec.Name,
		Description: cfg.description,
		Author:      cfg.author,
		ApprovedBy:  cfg.approvedBy,
		ApprovedAt:  cfg.approvedAt,
		Inputs:      inputs,
		Output:      out,
	}
	fnName := name
	apply := func(items map[K]In) (map[K]Out, error) {
		result := make(map[K]Out, len(items))
		for k, item := range items {
			v, err := fn.Apply(item)
			if err != nil {
				return nil, CollectionKeyError{Function: fnName, Key: fmt.Sprintf("%v", k), Err: err}
			}
			result[k] = v
		}
		return result, nil
	}
	var refinement func(map[K]In) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(v map[K]In) error { return r(v) }
	}
	return &Function[map[K]In, map[K]Out]{
		Spec:       spec,
		inputCodec: inputCodec,
		output:     outputCodec,
		apply:      apply,
		refinement: refinement,
	}
}
