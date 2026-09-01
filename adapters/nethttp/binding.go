package nethttp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── IngestAdapter ─────────────────────────────────────────────────────────────

// IngestAdapterOptions configures [IngestAdapter].
type IngestAdapterOptions struct {
	Options Options
	Buffer  int
}

// IngestAdapter returns a [ports.SourceAdapter] that accepts HTTP requests as
// pipeline items. When Activate is called it registers a handler with mux.
// Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(
//	    mux, ingestHandle, nethttp.IngestAdapterOptions{Buffer: 8}))
func IngestAdapter[T any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[T, struct{}],
	opts IngestAdapterOptions,
) ports.SourceAdapter[T] {
	return &nethttpIngestAdapter[T]{mux: mux, handle: handle, opts: opts}
}

type nethttpIngestAdapter[T any] struct {
	mux    *http.ServeMux
	handle *rest.RouteHandle[T, struct{}]
	opts   IngestAdapterOptions
}

func (a *nethttpIngestAdapter[T]) AdapterName() string { return "nethttp.IngestAdapter" }

func (a *nethttpIngestAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	ch := make(chan T, a.opts.Buffer)
	// Inline HandlerIngest logic: wrap opts.ErrorHandler for PipelineFullError → 503.
	wrappedOpts := a.opts.Options
	wrappedOpts.ErrorHandler = remapStatus(a.opts.Options.ErrorHandler, func(err error) int {
		var pfe PipelineFullError
		if isErrorAs(err, &pfe) {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	h := handlerFunc(a.handle, func(_ context.Context, req T) (struct{}, error) {
		select {
		case ch <- req:
			return struct{}{}, nil
		default:
			return struct{}{}, PipelineFullError{
				Path:     a.handle.Descriptor.Path,
				Capacity: cap(ch),
			}
		}
	}, wrappedOpts)
	a.mux.Handle(a.handle.Descriptor.Method+" "+a.handle.Descriptor.Path, h)

	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-ch:
				if !ok {
					return
				}
				select {
				case dst <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	<-ctx.Done()
	// Wait for the forwarding goroutine: Activate returning signals the port
	// that dst may be closed — a send racing with that close would panic.
	<-forwardDone
}

// isErrorAs is a type-assert helper used by binding.go only.
func isErrorAs[T any](err error, target *T) bool {
	if v, ok := err.(T); ok {
		*target = v
		return true
	}
	return false
}

// ── SSEAdapter ────────────────────────────────────────────────────────────────

// SSEAdapterOptions configures [SSEAdapter].
type SSEAdapterOptions struct {
	Options          Options
	SSEStreamOptions SSEStreamOptions
}

// SSEAdapter returns a [ports.SinkAdapter] that serves each item from the SinkPort
// as an SSE event to all connected clients. Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, nethttp.SSEAdapterOptions{}))
func SSEAdapter[Event any](
	mux *http.ServeMux,
	handle *rest.SSERouteHandle[struct{}, Event],
	opts SSEAdapterOptions,
) ports.SinkAdapter[Event] {
	return &nethttpSSEAdapter[Event]{mux: mux, handle: handle, opts: opts}
}

type nethttpSSEAdapter[Event any] struct {
	mux    *http.ServeMux
	handle *rest.SSERouteHandle[struct{}, Event]
	opts   SSEAdapterOptions
}

func (a *nethttpSSEAdapter[Event]) AdapterName() string { return "nethttp.SSEAdapter" }

func (a *nethttpSSEAdapter[Event]) Activate(ctx context.Context, src gstream.Stream[Event]) {
	hub := gstream.NewBroadcastHub(ctx, src, 32)
	sseOpts := a.opts.SSEStreamOptions
	if sseOpts.Topic == "" {
		sseOpts.Topic = a.handle.Descriptor.Path
	}
	fn := SSEFromHub[struct{}, Event](hub, sseOpts)
	// Calls sseHandlerFunc directly (not the deprecated RegisterSSE) —
	// no middleware Fn-shape validation needed here since no middleware
	// is attached at this call site.
	routeLabel := "GET " + a.handle.Descriptor.Path
	a.mux.Handle(routeLabel, sseHandlerFunc(a.handle, fn, a.opts.Options))
	<-ctx.Done()
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

// CallStreamOptions configures [CallAdapter].
type CallStreamOptions struct {
	// Vars substitutes {varName} placeholders in the route's path template.
	//
	// When nil, path/query/header/cookie vars are derived PER-ITEM from each
	// item's own merge-field-declared struct fields (the same convenience
	// [CallWithHandle] provides). When set to a non-nil map (including an
	// explicitly empty one), that map is used as-is for every request
	// (static vars only) — the escape hatch, unchanged from prior behavior.
	Vars     map[string]string
	CallOpts CallOptions
	// Buffer is the output Stream channel buffer size. Default 0.
	Buffer int
}

// CallAdapter returns a [ports.IOAdapter] that sends each item as an HTTP request,
// emitting responses downstream. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, nethttp.CallAdapter(
//	    httpClient, "http://svc:8080", calibHandle, nethttp.CallStreamOptions{}))
func CallAdapter[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	opts CallStreamOptions,
) ports.IOAdapter[Req, Resp] {
	return &nethttpCallAdapter[Req, Resp]{client: client, baseURL: baseURL, handle: handle, opts: opts}
}

type nethttpCallAdapter[Req, Resp any] struct {
	client  *http.Client
	baseURL string
	handle  *rest.RouteHandle[Req, Resp]
	opts    CallStreamOptions
}

func (a *nethttpCallAdapter[Req, Resp]) AdapterName() string { return "nethttp.CallAdapter" }

func (a *nethttpCallAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	values := make(chan Resp, a.opts.Buffer)
	errs := make(chan error, a.opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case req, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				var resp Resp
				var err error
				if a.opts.Vars == nil {
					resp, err = CallWithHandle(ctx, a.client, a.baseURL, a.handle, req, a.opts.CallOpts)
				} else {
					resp, err = callWithVars(ctx, a.client, a.baseURL, a.handle, req, a.opts.Vars, a.opts.CallOpts)
				}
				if err != nil {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[Resp]{Values: values, Errors: errs}
}

// ── PollAdapter ───────────────────────────────────────────────────────────────

// PollStreamOptions configures [PollAdapter].
type PollStreamOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's path template.
	// The same map is used for every poll (static path vars only).
	Vars     map[string]string
	Observer stats.Observer
	Buffer   int
}

// PollAdapter returns a [ports.SourceAdapter] that polls an HTTP endpoint at
// interval, emitting each response. Use with [ports.SourcePort.Bind]:
//
//	domain.Configs.Bind(ctx, nethttp.PollAdapter(
//	    client, "http://config-svc", configHandle, ConfigReq{}, 5*time.Minute, nethttp.PollStreamOptions{}))
func PollAdapter[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	req Req,
	interval time.Duration,
	opts PollStreamOptions,
) ports.SourceAdapter[Resp] {
	return &nethttpPollAdapter[Req, Resp]{
		client: client, baseURL: baseURL, handle: handle,
		req: req, interval: interval, opts: opts,
	}
}

type nethttpPollAdapter[Req, Resp any] struct {
	client   *http.Client
	baseURL  string
	handle   *rest.RouteHandle[Req, Resp]
	req      Req
	interval time.Duration
	opts     PollStreamOptions
}

func (a *nethttpPollAdapter[Req, Resp]) AdapterName() string { return "nethttp.PollAdapter" }

func (a *nethttpPollAdapter[Req, Resp]) Activate(ctx context.Context, dst chan<- Resp, errs chan<- error) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	values := make(chan Resp, a.opts.Buffer)
	errChan := make(chan error, a.opts.Buffer)
	go func() {
		defer close(values)
		defer close(errChan)
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				resp, err := callWithVars(ctx, a.client, a.baseURL, a.handle, a.req, a.opts.Vars, CallOptions{Observer: obs})
				if err != nil {
					select {
					case errChan <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	valCh := values
	errCh := errChan
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			select {
			case dst <- v:
			case <-ctx.Done():
				return
			}
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			select {
			case errs <- e:
			case <-ctx.Done():
				return
			}
		}
	}
}

// ── DrainCallAdapter ──────────────────────────────────────────────────────────

// DrainCallOptions configures [DrainCallAdapter].
type DrainCallOptions struct {
	// Vars substitutes {varName} placeholders in the route's path template.
	//
	// When nil, path/query/header/cookie vars are derived PER-ITEM from each
	// item's own merge-field-declared struct fields (the same convenience
	// [CallWithHandle] provides) — every item may resolve to a different
	// concrete path/query/header/cookie set. When set to a non-nil map
	// (including an explicitly empty one), that map is used as-is for every
	// item (today's static-vars behavior, unchanged) — this remains the
	// escape hatch for routes with no merge fields or a route shared across
	// unrelated Req shapes.
	Vars     map[string]string
	OnError  func(error)
	CallOpts CallOptions
}

// DrainCallAdapter returns a [ports.SinkAdapter] that calls an HTTP endpoint
// for each item (fire-and-forget; response discarded). Use with [ports.SinkPort.Bind]:
//
//	domain.Events.Bind(ctx, nethttp.DrainCallAdapter(
//	    client, "http://audit-svc", auditHandle, nethttp.DrainCallOptions{}))
func DrainCallAdapter[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	opts DrainCallOptions,
) ports.SinkAdapter[Req] {
	return &nethttpDrainCallAdapter[Req, Resp]{client: client, baseURL: baseURL, handle: handle, opts: opts}
}

type nethttpDrainCallAdapter[Req, Resp any] struct {
	client  *http.Client
	baseURL string
	handle  *rest.RouteHandle[Req, Resp]
	opts    DrainCallOptions
}

func (a *nethttpDrainCallAdapter[Req, Resp]) AdapterName() string { return "nethttp.DrainCallAdapter" }

func (a *nethttpDrainCallAdapter[Req, Resp]) Activate(ctx context.Context, src gstream.Stream[Req]) {
	onErr := a.opts.OnError
	gstream.Drain(ctx, src,
		func(ctx context.Context, item Req) error {
			var err error
			if a.opts.Vars == nil {
				_, err = CallWithHandle(ctx, a.client, a.baseURL, a.handle, item, a.opts.CallOpts)
			} else {
				_, err = callWithVars(ctx, a.client, a.baseURL, a.handle, item, a.opts.Vars, a.opts.CallOpts)
			}
			if err != nil {
				if onErr != nil {
					onErr(err)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{Observer: a.opts.CallOpts.Observer},
	)
}

// ── PipelineAdapter ───────────────────────────────────────────────────────────

// PipelineAdapterOptions configures [PipelineAdapter].
type PipelineAdapterOptions struct {
	Options Options
}

// PipelineAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an HTTP endpoint via [PipelineHandler]. When [ports.ToolPort.Bind]
// is called it registers the handler with mux. Use with [ports.ToolPort.Bind]:
//
//	domain.OEEToolPort.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle,
//	    nethttp.PipelineAdapterOptions{}))
func PipelineAdapter[Req, Resp any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[Req, Resp],
	opts PipelineAdapterOptions,
) ports.ToolAdapter[Req, Resp] {
	return &nethttpPipelineAdapter[Req, Resp]{mux: mux, handle: handle, opts: opts}
}

type nethttpPipelineAdapter[Req, Resp any] struct {
	mux    *http.ServeMux
	handle *rest.RouteHandle[Req, Resp]
	opts   PipelineAdapterOptions
}

func (a *nethttpPipelineAdapter[Req, Resp]) AdapterName() string { return "nethttp.PipelineAdapter" }

func (a *nethttpPipelineAdapter[Req, Resp]) Bind(
	_ context.Context,
	fn func(context.Context, Req) gstream.Stream[Resp],
) error {
	RegisterPipeline(a.mux, a.handle, func(ctx context.Context, req Req) gstream.Stream[Resp] {
		return fn(ctx, req)
	}, a.opts.Options)
	return nil
}

// ── LatestAdapter ─────────────────────────────────────────────────────────────

// LatestAdapter returns a [ports.LatestAdapter] that serves a
// [ports.LatestPort]'s cached value as a GET endpoint — the port-based
// successor to [HandlerLatest]/[RegisterLatest] (which own their own cache
// cell; the port owns it here). Use with [ports.LatestPort.Bind]:
//
//	handle, _ := domain.Latest.PluginRESTPattern(domain.LatestPattern)
//	must(domain.Latest.Bind(ctx, nethttp.LatestAdapter(mux, handle, nethttp.Options{})))
//	go domain.Latest.Feed(ctx, readings)
//
// Before the first value arrives the handler responds 503 Service Unavailable
// with [NoLatestValueError] (same semantics as [HandlerLatest]). All codec
// layers (params, security) validate exactly as with the regular request pipeline.
func LatestAdapter[Resp any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[struct{}, Resp],
	opts Options,
) ports.LatestAdapter[Resp] {
	return &nethttpLatestAdapter[Resp]{mux: mux, handle: handle, opts: opts}
}

type nethttpLatestAdapter[Resp any] struct {
	mux    *http.ServeMux
	handle *rest.RouteHandle[struct{}, Resp]
	opts   Options
}

func (a *nethttpLatestAdapter[Resp]) AdapterName() string { return "nethttp.LatestAdapter" }

func (a *nethttpLatestAdapter[Resp]) Serve(_ context.Context, latest func() (Resp, bool)) error {
	wrappedOpts := a.opts
	wrappedOpts.ErrorHandler = remapStatus(a.opts.ErrorHandler,
		func(err error) int {
			var nlv NoLatestValueError
			if errors.As(err, &nlv) {
				return http.StatusServiceUnavailable
			}
			return 0
		})
	h := handlerFunc(a.handle, func(_ context.Context, _ struct{}) (Resp, error) {
		v, ok := latest()
		if !ok {
			var zero Resp
			return zero, NoLatestValueError{Path: a.handle.Descriptor.Path}
		}
		return v, nil
	}, wrappedOpts)
	a.mux.Handle(a.handle.Descriptor.Method+" "+a.handle.Descriptor.Path, h)
	return nil // registration-style Serve: returns immediately
}

// ── Consume + CallSSEAdapter ───────────────────────────────────────────────

// ConsumeOptions configures [Consume]/[CallSSEAdapter].
type ConsumeOptions struct {
	// QueryParams/CookieParams/HeaderParams/ExtraHeaders — same shape and
	// precedence as [CallOptions] (explicit wins over Req-derived).
	QueryParams  map[string]string
	CookieParams map[string]string
	HeaderParams map[string]string
	ExtraHeaders http.Header

	// MaxBackoff caps the exponential reconnect backoff. Default 30s
	// (initial step 250ms, doubling per consecutive failure, reset after
	// a connection that delivered at least one successfully-decoded
	// event) — identical shape to [adapters/websocket.DialAdapterOptions].
	MaxBackoff time.Duration

	// OnError is called for EVERY non-fatal failure: a failed connection
	// attempt ([SSEConnectError]), a malformed event ([SSEParseError]),
	// or fn returning a non-nil error for one event ([SSEHandlerError]).
	// Consumption always continues after each — mirrors
	// [mqtt5.Subscribe]/[zeromq.Subscribe]'s existing OnError convention
	// exactly. nil is a valid no-op default.
	OnError func(error)

	// OnCredentialRejected, when non-nil, is called when a reconnect
	// attempt gets HTTP 401 AND a [rest.SSERoute.ClientMW]-declared
	// credential was attached to that attempt — mirrors
	// [CallOptions.OnCredentialRejected] exactly, same trigger condition
	// (401 + engaged credential fn). MORE important here than for Call:
	// since Consume retries forever, a caching credential wrapper with no
	// way to invalidate would otherwise resend the SAME rejected
	// credential on every subsequent reconnect attempt, forever.
	OnCredentialRejected func()

	// Observer receives RecordRequest per connect attempt. Resolved from
	// ctx when nil.
	Observer stats.Observer

	// Formats, when non-nil, OVERRIDES the route's declared event decode
	// format for THIS Consume/CallSSEAdapter call only. Type-erased
	// ([]format.Format[Event]) since ConsumeOptions itself is not
	// generic; [Consume]/[CallSSEAdapter] type-assert it once Event is
	// concrete, returning [CallFormatOptError] on a type mismatch — same
	// resolution/priority contract as [CallOptions.ResponseFormats].
	//
	// Priority: Formats (this field) > handle.Formats (declared via
	// [rest.Formats] on the route) > handle.DecodeEvent (JSON default).
	Formats any
}

func (o ConsumeOptions) maxBackoff() time.Duration {
	if o.MaxBackoff <= 0 {
		return 30 * time.Second
	}
	return o.MaxBackoff
}

const sseInitialBackoff = 250 * time.Millisecond

// consumeSSE is the SHARED, unexported connect+decode+reconnect loop both
// [Consume] and [CallSSEAdapter] delegate to — mirrors
// [zeromq.SubscribeAdapter]'s documented "Activate delegates to Subscribe"
// relationship exactly. BLOCKS until ctx is cancelled or a fatal
// pre-connect setup error occurs (e.g. merge-field encoding failure).
//
// The merge-field vars AND the ClientMW credential are BOTH RE-DERIVED on
// EVERY reconnect attempt, never cached across attempts — the correct
// analogue of [Call]'s "derive fresh per invocation," since one call to
// consumeSSE spans many reconnects.
func consumeSSE[Req, Event any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.SSERouteHandle[Req, Event],
	req Req,
	fn func(context.Context, Event) error,
	opts ConsumeOptions,
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	if err := validateClientImplementationShapes(handle.ClientImplementations); err != nil {
		return err
	}

	onErr := func(err error) {
		if opts.OnError != nil {
			opts.OnError(err)
		}
	}

	backoff := sseInitialBackoff
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		attempt++
		hadTraffic, err := consumeSSEOnce(ctx, client, baseURL, handle, req, fn, opts, obs)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			onErr(SSEConnectError{URL: baseURL + handle.Descriptor.Path, Attempt: attempt, Err: err})
		}
		if hadTraffic {
			backoff = sseInitialBackoff
			attempt = 0
			continue
		}
		// Wait with the CURRENT backoff value FIRST, then double for next
		// time — mirrors adapters/websocket's dialLoop exactly: the first
		// wait after a failure uses the initial step, not an already-
		// doubled value.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > opts.maxBackoff() {
			backoff = opts.maxBackoff()
		}
	}
}

// consumeSSEOnce performs ONE connection attempt: derives vars/credential
// fresh, connects, resolves the decode format, reads and dispatches events
// until the connection drops or ctx is cancelled. Returns whether at least
// one event was successfully decoded (hadTraffic, resets backoff) and any
// connect-phase error (nil on a clean drop or ctx cancellation).
func consumeSSEOnce[Req, Event any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.SSERouteHandle[Req, Event],
	req Req,
	fn func(context.Context, Event) error,
	opts ConsumeOptions,
	obs stats.Observer,
) (hadTraffic bool, connectErr error) {
	start := time.Now()
	path := handle.Descriptor.Path
	method := http.MethodGet

	// 1. Derive path/query/header/cookie vars from req, fresh every attempt.
	pathVars, err := codex.EncodeVars(req, handle.PathMergeFields()...)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	query, err := codex.EncodeVars(req, handle.QueryMergeFields()...)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	headers, err := codex.EncodeVars(req, handle.HeaderMergeFields()...)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	cookies, err := codex.EncodeVars(req, handle.CookieMergeFields()...)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	queryParams := overrideDerived(query, opts.QueryParams)
	headerParams := overrideDerived(headers, opts.HeaderParams)
	cookieParams := overrideDerived(cookies, opts.CookieParams)

	concretePath, err := handle.BuildPath(pathVars)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}

	rawURL := strings.TrimRight(baseURL, "/") + concretePath
	if len(queryParams) > 0 {
		qv := make(url.Values, len(queryParams))
		for k, v := range queryParams {
			qv.Set(k, v)
		}
		rawURL += "?" + qv.Encode()
	}

	// 2. Resolve security requirements and derive a FRESH credential —
	// re-run every attempt, never cached across reconnects.
	secReqs := handle.Descriptor.Security
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	var credHeaders http.Header
	var credentialFnRan bool
	if len(secReqs) > 0 {
		credHeaders, credentialFnRan, err = mergeCredentialHeaders(ctx, secReqs, handle.ClientImplementations)
		if err != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			return false, err
		}
	}

	// Resolve per-call format override (opts.Formats), falling back to
	// the route-declared handle.Formats when no override was given for
	// this Consume/CallSSEAdapter call — see [ConsumeOptions.Formats].
	eventFormats, err := resolveCallFormat[Event](handle.Formats, opts.Formats, "response")
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, RequestBuildError{Err: err}
	}
	// Set Accept from the first resolved Format, mirroring Call's own
	// "Accept from registered response formats" behavior — this is the
	// signal resolveSSEDecodeFormat reads back to pick the SAME decoder
	// the server's own Accept-negotiation would have chosen against.
	if len(eventFormats) > 0 {
		if ct := eventFormats[0].ContentType(); ct != "" {
			httpReq.Header.Set("Accept", ct)
		}
	}
	for k, v := range headerParams {
		httpReq.Header.Set(k, v)
	}
	for k, vs := range opts.ExtraHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, vs := range credHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, v := range cookieParams {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	if len(secReqs) > 0 && len(credHeaders) > 0 {
		if credErr := validateSecurityCredentials(httpReq, secReqs, handle.SecuritySchemes); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(path, firstScheme(secReqs))
			}
			obs.RecordRequest(method, path, 0, time.Since(start))
			return false, credErr
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	defer resp.Body.Close()
	obs.RecordRequest(method, path, resp.StatusCode, time.Since(start))

	if resp.StatusCode == http.StatusUnauthorized {
		if credentialFnRan && opts.OnCredentialRejected != nil {
			opts.OnCredentialRejected()
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, UnexpectedStatusError{
			Method: method, Path: path, StatusCode: resp.StatusCode, Body: body, Header: resp.Header,
		}
	}

	// 3. Resolve the decode format ONCE for this connection — mirrors the
	// SAME selection algorithm the server runs against the Accept header
	// (see negotiateFormatReflect in serve_sse.go): the response's overall
	// Content-Type is ALWAYS "text/event-stream" (never reveals which
	// Formats entry the server chose), so the client instead resolves via
	// the SAME "what Accept did I send" logic the server negotiates
	// against — both sides independently agree without a round-trip.
	decode := resolveSSEDecodeFormat(handle, eventFormats, httpReq.Header.Get("Accept"))

	// 4. Read "data:" lines until EOF/drop/cancellation. Per the WHATWG
	// Server-Sent Events spec, ONE event may span MULTIPLE consecutive
	// "data:" lines (e.g. YAML block-style output, or a multi-line HTML
	// fragment) — each is accumulated (prefix stripped) and joined with
	// "\n"; the event dispatches at the next blank line, exactly
	// reversing writeSSEData's per-line framing on the server side
	// (bytes.Split/join around "\n" is a lossless round-trip regardless
	// of binary vs. text content).
	scanner := bufio.NewScanner(resp.Body)
	var dataLines []string
	dispatch := func() {
		if len(dataLines) == 0 {
			return
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		event, decErr := decode(data)
		if decErr != nil {
			if opts.OnError != nil {
				opts.OnError(SSEParseError{URL: rawURL, Line: data, Err: decErr})
			}
			return
		}
		if fnErr := fn(ctx, event); fnErr != nil {
			if opts.OnError != nil {
				opts.OnError(SSEHandlerError{URL: rawURL, Err: fnErr})
			}
		}
		hadTraffic = true
	}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return hadTraffic, nil
		}
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			dataLines = append(dataLines, data)
			continue
		}
		if line == "" {
			dispatch() // blank line — dispatch any accumulated event
			continue
		}
		// Non-"data:" field lines (e.g. a future id:/event:/retry: line —
		// see docs/roadmap/sse-resume-and-retry-policy.md) are ignored,
		// same as before.
	}
	dispatch() // a final event with no trailing blank line (EOF/drop)
	return hadTraffic, nil
}

// resolveSSEDecodeFormat picks the ONE decode function to use for every
// event on a connection, mirroring the server's Accept-negotiation
// algorithm exactly (empty/"*/*" Accept → first declared Format; a
// specific Accept → matching ContentType; no match/no Formats declared →
// handle.DecodeEvent, the JSON default).
func resolveSSEDecodeFormat[Req, Event any](handle *rest.SSERouteHandle[Req, Event], eventFormats []format.Format[Event], accept string) func(string) (Event, error) {
	if len(eventFormats) > 0 {
		if accept == "" || accept == "*/*" {
			f := eventFormats[0]
			return func(data string) (Event, error) { return f.Unmarshal([]byte(data)) }
		}
		for _, part := range strings.Split(accept, ",") {
			want := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
			if want == "*/*" {
				f := eventFormats[0]
				return func(data string) (Event, error) { return f.Unmarshal([]byte(data)) }
			}
			for _, f := range eventFormats {
				ct, _, _ := strings.Cut(f.ContentType(), ";")
				if strings.TrimSpace(ct) == want {
					f := f
					return func(data string) (Event, error) { return f.Unmarshal([]byte(data)) }
				}
			}
		}
	}
	return func(data string) (Event, error) { return handle.DecodeEvent([]byte(data)) }
}

// Consume opens a long-lived SSE connection to sseRoute against
// consumer's baseURL — the STRICT equivalent of [Call] for a stream of
// many events instead of one Resp. BLOCKS until ctx is cancelled or a
// fatal connection-setup error occurs — mirrors [zeromq.Subscribe]'s
// blocking shape exactly, so the CALLER'S OWN goroutine must run the loop
// (typically via `go nethttp.Consume(...)`).
//
// fn is called once per decoded Event. fn's returned error is NON-FATAL:
// wrapped in [SSEHandlerError] and reported via opts.OnError, then
// consumption continues with the next event.
func Consume[Req, Event any](
	ctx context.Context,
	consumer *Consumer,
	sseRoute rest.SSERoute[Req, Event],
	req Req,
	fn func(ctx context.Context, event Event) error,
	opts ConsumeOptions,
) error {
	handle := sseRoute.ClientHandle()
	return consumeSSE(ctx, consumer.client, consumer.baseURL, handle, req, fn, opts)
}

// CallSSEAdapter returns a [ports.SourceAdapter] that DELEGATES to the
// SAME underlying connect+decode+reconnect loop [Consume] uses. Takes a
// pre-built *rest.SSERouteHandle (not a bare [rest.SSERoute]) — matching
// [CallAdapter]/[DrainCallAdapter]'s existing handle-based convention.
// Deliberately stays on PLAIN client/baseURL parameters, NOT a [Consumer]
// — matching [CallAdapter]/[DrainCallAdapter]'s existing precedent. Not to
// be confused with [SSEAdapter], the SERVER-side counterpart that SERVES
// events out — CallSSEAdapter CONSUMES a remote SSE endpoint as a client.
// Use with [ports.SourcePort.Bind]:
//
//	port, _ := ports.NewSourcePort[Event]("sseEvents", eventCodec, ports.PortOptions{})
//	port.Bind(ctx, nethttp.CallSSEAdapter(httpClient, baseURL, sseHandle, req, opts))
//	events := port.Stream(ctx)
func CallSSEAdapter[Req, Event any](
	client *http.Client,
	baseURL string,
	handle *rest.SSERouteHandle[Req, Event],
	req Req,
	opts ConsumeOptions,
) ports.SourceAdapter[Event] {
	return &nethttpCallSSEAdapter[Req, Event]{client: client, baseURL: baseURL, handle: handle, req: req, opts: opts}
}

type nethttpCallSSEAdapter[Req, Event any] struct {
	client  *http.Client
	baseURL string
	handle  *rest.SSERouteHandle[Req, Event]
	req     Req
	opts    ConsumeOptions
}

func (a *nethttpCallSSEAdapter[Req, Event]) AdapterName() string { return "nethttp.CallSSEAdapter" }

func (a *nethttpCallSSEAdapter[Req, Event]) Activate(ctx context.Context, dst chan<- Event, errs chan<- error) {
	opts := a.opts
	opts.OnError = func(err error) {
		select {
		case errs <- err:
		case <-ctx.Done():
		default:
		}
		if a.opts.OnError != nil {
			a.opts.OnError(err)
		}
	}
	fn := func(_ context.Context, e Event) error {
		select {
		case dst <- e:
		case <-ctx.Done():
		}
		return nil
	}
	_ = consumeSSE(ctx, a.client, a.baseURL, a.handle, a.req, fn, opts)
}
