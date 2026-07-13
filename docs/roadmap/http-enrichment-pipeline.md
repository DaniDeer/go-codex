# HTTP Enrichment Pipeline — Design Evaluation

> **Status:** ✅ Fully implemented — all steps work today.
> [← Back to Roadmap](index.md)

---

## Use case

1. Incoming HTTP request triggers a pipeline (`PipelineHandler`)
2. Forge functions perform initial computation on the request
3. Mid-pipeline: call an external HTTP service and receive its response
4. Final forge function combines the pre-call result + external response into the final answer
5. Return the response to the original caller
6. Optional: read a JSON file as an intermediate data source; write or patch a JSON file as a sink

---

## What works today

### Step 1 — Incoming REST request triggers pipeline

✅ **`nethttp.PipelineHandler`** is the entry point:

```go
nethttp.RegisterPipeline(mux, incomingHandle,
    func(ctx context.Context, req IncomingReq) stream.Stream[FinalResp] {
        s := stream.Single(ctx, req)   // wrap request as one-shot pipeline source
        // ... pipeline steps ...
        return finalStream
    },
    nethttp.Options{})
```

All codec layers run: body codec, path/query/cookie/header params, security. The first
emitted value from the returned stream becomes the HTTP response.

### Step 2 — Initial forge computation

✅ **`stream.Apply`** applies a forge function per item:

```go
type IntermediaryData struct {
    OriginalReq  IncomingReq
    Computed     ComputedValue
}

s1 := stream.Apply(ctx, s, initialCalcFn, stream.ApplyOptions{})
// s1: Stream[IntermediaryData]
```

### Step 3 — Mid-pipeline HTTP call

✅ **`nethttp.CallStream` is now implemented.** This step works declaratively:

**Do not put `nethttp.Call` inside a forge function.** Forge functions must be pure.
Use `nethttp.CallStream` as the explicit I/O step:

```go
// ✅ Correct — declarative I/O step:
s2 := nethttp.CallStream(ctx, httpClient, "http://enrichment-svc",
    enrichHandle, s1,
    nethttp.CallStreamOptions{CallOpts: nethttp.CallOptions{}})
// s2: Stream[EnrichedData] — each item is the external service's response
```

### Step 4 — Combine pre-call + post-call results

✅ **State is carried through the pipeline struct.** The bridge operator emits a
combined value that forge functions at the next step can use:

```go
type EnrichedData struct {
    Initial  IntermediaryData  // from before the HTTP call
    External ExternalResponse  // from the HTTP response
}

// Pure forge function — receives both as explicit typed inputs:
finalFn := forge.NewFunction("computeFinal", "1.0.0",
    enrichedCodec, finalResponseCodec,
    func(d EnrichedData) (FinalResp, error) {
        return combine(d.Initial.Computed, d.External), nil
    },
)
finalStream := stream.Apply(ctx, s2, finalFn, stream.ApplyOptions{})
```

### Step 5 — Return response

✅ **`PipelineHandler` collects the first value** from the returned stream and
writes it as the HTTP response body. No extra code needed.

### Full pipeline sketch

```go
nethttp.RegisterPipeline(mux, incomingHandle,
    func(ctx context.Context, req IncomingReq) stream.Stream[FinalResp] {
        s  := stream.Single(ctx, req)
        s1 := stream.Apply(ctx, s,  initialCalcFn, stream.ApplyOptions{})  // pure computation
        s2 := nethttp.CallStream(ctx, httpClient, baseURL, enrichHandle, s1,
            nethttp.CallStreamOptions{})                                    // explicit I/O step
        return stream.Apply(ctx, s2, computeFinalFn, stream.ApplyOptions{}) // pure computation
    },
    nethttp.Options{})
```

---

## File I/O in the pipeline

See [Stream Bridge Guide — `format.File` typed whole-file operations](../guides/stream-bridges.md)
for the full read / write / patch reference with code examples.

### Why `format.File` does not need dedicated stream bridges

The existing stream bridges in `adapters/file` succeed because they are inherently streaming:

| Bridge | Why a bridge makes sense |
|--------|-------------------------|
| `ScanStream` | Reads **N items** from a file (one per line) — the stream lifecycle (open → iterate → close) is non-trivial to wire manually |
| `WatchStream` | Produces a **continuous stream** of file-system events over time |
| `DrainWrite` | Writes **N items** to a writer, one per stream item, managing the open lifecycle |

`format.File[T]` operations (`Read`, `Write`, `Patch`, `Update`) are **point-in-time, single-value operations**. They read or write exactly one typed value per call. There is no multi-item streaming lifecycle to manage. A hypothetical bridge would add trivial wrappers:

```go
// Hypothetical ReadStream — barely useful:
stream.Single(ctx, config)   // equivalent — two lines in the caller
                             // vs. a dedicated bridge function

// Hypothetical DrainFileWrite — saves ~3 lines; adds a function with ~3 lines inside:
stream.Drain(ctx, src, func(ctx context.Context, v T) error {
    return f.Write(vars, v, format.FileOptions{Context: ctx})
}, logErr, stream.DrainOptions{})  // caller writes this directly
```

`TapWriteFile` and `DrainWriteFile` are now available as first-class declarative bridges. Use `stream.Tap` + `format.File.Write` directly for full control, or the bridge helpers for concise wiring:

```go
// Declarative whole-file sink (stream continues flowing):
results = file.TapWriteFile(ctx, resultFile, results, nil, file.TapWriteFileOptions{OnError: logErr})

// Terminal whole-file sink:
file.DrainWriteFile(ctx, resultFile, src, nil, file.DrainWriteFileOptions{OnError: logErr})
```

For "reload on change" (dynamic config), use `WatchStream + FlatMapSlice`:
```go
changes := file.WatchStream(ctx, "/config/", 500*time.Millisecond, srcOpts)
configs := stream.FlatMapSlice(ctx, changes, func(path string) []Config {
    v, err := configFile.Read(nil, format.FileOptions{Context: ctx})
    if err != nil { return nil }
    return []Config{v}
})
```

---

## `nethttp.CallStream` ✅ Implemented

> See also: [Declarative I/O Steps](declarative-io-steps.md) — transport matrix and API details.

`nethttp.CallStream` is implemented and follows the same pattern as `zeromq.CallStream`
and `mqtt5.CallStream`. It makes the HTTP enrichment step a first-class declarative I/O
step — forge functions remain pure:

```go
// ✅ All three transports now have CallStream:
responses := zeromq.CallStream(ctx, sock, handle, requestStream, opts)
responses := mqtt5.CallStream(ctx, client, router, handle, requestStream, opts)
enriched  := nethttp.CallStream(ctx, httpClient, baseURL, enrichHandle, src, opts)
```

Each item is sent as a typed request; the decoded response is emitted as the next
stream item. Errors go to `Stream.Errors` as typed transport errors.

**Forge purity principle:** Forge functions must be pure domain computations with zero
I/O. `nethttp.CallStream` is the correct intermediate I/O step — never put
`nethttp.Call` inside a forge function.

---

## Summary table

| Step | Possible today? | API |
|------|----------------|-----|
| Incoming REST → pipeline | ✅ | `nethttp.PipelineHandler` / `RegisterPipeline` |
| Forge computation in pipeline | ✅ pure | `stream.Apply` + `forge.NewFunction` (zero I/O) |
| HTTP call mid-pipeline | ✅ | `nethttp.CallStream(ctx, client, baseURL, handle, src, opts)` |
| Combine pre/post-HTTP results | ✅ | carry state through pipeline structs (bridge emits combined value) |
| Return final response | ✅ | `PipelineHandler` collects first stream value |
| Read config file (static) | ✅ | Load at startup → pass as forge function input via `CombineLatest2` |
| Read config file (dynamic) | ✅ | `WatchStream` + `FlatMapSlice` → `CombineLatest2` |
| Write whole JSON file (sink) | ✅ | `file.TapWriteFile` / `file.DrainWriteFile` (or `stream.Tap` + `format.File.Write`) |
| Patch JSON file (sink) | ✅ | `format.File.Patch` / `format.PatchEncoded` inside `stream.Tap` |
| NDJSON file append (sink) | ✅ | `adapters/file.DrainWrite` |


