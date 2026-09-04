package nethttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/stats"
)

// restPkgPath is api/rest's import path — used to distinguish a genuine
// rest.Route[Req,Resp] value (for ANY Req,Resp) from an unrelated/
// wrong-package value passed by caller mistake to [rest.Client.Call].
const restPkgPath = "github.com/DaniDeer/go-codex/api/rest"

// clientTransport implements [rest.ClientTransport], wrapping an internal
// [*caller] — built by [Attach]. See
// docs/roadmap/transport-agnostic-serve-interface.md for the full design
// and the reflection technique this type relies on (Go forbids generic
// methods, so Call recovers the concrete Req/Resp types at runtime via
// reflection against the ALREADY-CONCRETE closures on the route's
// type-erased *rest.RouteHandle — never via reflecting a generic
// FUNCTION, which Go does not support).
type clientTransport struct {
	caller *caller
}

// Attach binds httpClient+baseURL (via an internal [*caller]) as client's
// [rest.ClientTransport] — the "attach the adapter to the client" step
// behind [rest.Client.Call]. Returns
// [rest.ClientTransportAlreadyAttachedError] if client already has a
// transport attached.
//
// NOTE — v1 scope: the reflection shim's Call covers the CORE common
// case (JSON body encode/decode, no path/query/header/cookie params, no
// security/credential handling, no per-call format override). Per
// Decision 6 (see docs/design/d-0002-pubsub-workflow-simplification.md),
// [Attach] is the sole public entry point built on the now-unexported
// `call` primitive; a caller needing the omitted features (or a
// pre-built *rest.RouteHandle, e.g. adapters/mcprest bridging a REST
// route into another protocol) uses the still-public [CallWithHandle]
// directly instead. [stats.Observer] (RecordRequest, TraceObserver) and
// a declared [rest.ErrorPattern]'s client-side typed-error decode
// (via [rest.RouteHandle.DecodeErrorFor]) ARE fully wired — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's addendum on
// Client.Attach Observer/ErrorPattern parity for the fix history.
//
//	client := rest.NewClient()
//	if err := nethttp.Attach(client, httpClient, baseURL); err != nil { ... }
//	respAny, err := client.Call(ctx, getUserRoute, GetUserReq{ID: "f47ac10b"})
//	resp := respAny.(GetUserResp)
func Attach(client *rest.Client, httpClient *http.Client, baseURL string) error {
	return client.Attach(&clientTransport{caller: newCaller(httpClient, baseURL)})
}

// Call implements [rest.ClientTransport]. See [Attach]'s doc comment for
// v1 scope notes. Resolves [stats.Observer] from ctx (this shim has no
// per-call Options struct to carry an explicit override) and calls
// RecordRequest on EVERY exit path — status 0 before a response is ever
// received (pre-flight/build/network failures), the real status code
// once received — mirroring [callWithVars]'s own convention exactly.
func (t *clientTransport) Call(ctx context.Context, routeAny, reqAny any) (result any, err error) {
	obs := stats.ObserverFromContext(ctx)
	start := time.Now()

	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() || rv.Type().PkgPath() != restPkgPath || !strings.HasPrefix(rv.Type().Name(), "Route[") {
		obs.RecordRequest("", "", 0, time.Since(start))
		err = rest.TransportTypeMismatchError{Want: "rest.Route[Req, Resp]", Got: fmt.Sprintf("%T", routeAny)}
		return nil, err
	}
	handleResults := rv.MethodByName("ClientHandle").Call(nil)
	handleVal := handleResults[0]
	elem := handleVal.Elem()

	descriptor := elem.FieldByName("Descriptor")
	method := strings.ToUpper(descriptor.FieldByName("Method").String())
	path := descriptor.FieldByName("Path").String()

	encodeReqWithFormatsMethod := handleVal.MethodByName("EncodeRequestWithFormats") // func(Req, ...format.Format[Req]) ([]byte, string, error)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != encodeReqWithFormatsMethod.Type().In(0) {
		obs.RecordRequest(method, path, 0, time.Since(start))
		err = rest.TransportTypeMismatchError{
			Path: path, Want: encodeReqWithFormatsMethod.Type().In(0).String(), Got: fmt.Sprintf("%T", reqAny),
		}
		return nil, err
	}

	// Named return `err` lets this deferred EndSpan see whichever error (if
	// any) the function ultimately returns, mirroring [callWithVars]'s own
	// TraceObserver wiring exactly.
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "http.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	buildPathResults := handleVal.MethodByName("BuildPath").Call([]reflect.Value{reflect.ValueOf(map[string]string(nil))})
	if errI, _ := buildPathResults[1].Interface().(error); errI != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		err = errI
		return nil, err
	}
	concretePath, _ := buildPathResults[0].Interface().(string)

	// The route's OWN declaration (WithRequestFormats) is the single
	// source of truth for which format applies — EncodeRequestWithFormats
	// resolves it; Client.Attach never duplicates that resolution logic
	// itself (no call-time override to pass, matching this shim's
	// documented v1 scope).
	var body io.Reader
	var contentType string
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		encodeResults := encodeReqWithFormatsMethod.Call([]reflect.Value{reqVal})
		if errI, _ := encodeResults[2].Interface().(error); errI != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = fmt.Errorf("nethttp: encode request: %w", errI)
			return nil, err
		}
		bodyBytes, _ := encodeResults[0].Interface().([]byte)
		contentType, _ = encodeResults[1].Interface().(string)
		body = bytes.NewReader(bodyBytes)
	}

	rawURL := strings.TrimRight(t.caller.baseURL, "/") + concretePath
	httpReq, buildErr := http.NewRequestWithContext(ctx, method, rawURL, body)
	if buildErr != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		err = RequestBuildError{Err: buildErr}
		return nil, err
	}
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}

	// Set Accept header from the route's declared response format, same
	// single source of truth as the request side.
	acceptContentType := "application/json"
	responseFormatResults := handleVal.MethodByName("ResponseFormat").Call(nil)
	if ok, _ := responseFormatResults[1].Interface().(bool); ok {
		if ct, _ := responseFormatResults[0].MethodByName("ContentType").Call(nil)[0].Interface().(string); ct != "" {
			acceptContentType = ct
		}
	}
	httpReq.Header.Set("Accept", acceptContentType)

	resp, doErr := t.caller.client.Do(httpReq)
	if doErr != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		err = RequestError{Method: method, Path: path, Err: doErr}
		return nil, err
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	obs.RecordRequest(method, path, statusCode, time.Since(start))

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		err = ResponseBodyError{Err: readErr}
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		// Prefer a declared ErrorPattern's client-side typed-error decode
		// over the untyped fallback when one matches — mirrors
		// [callWithVars]'s own step 11 exactly.
		decodeErrResults := handleVal.MethodByName("DecodeErrorFor").Call([]reflect.Value{
			reflect.ValueOf(statusCode), reflect.ValueOf(respBody),
		})
		errResp, _ := decodeErrResults[0].Interface().(rest.ErrorPatternResponse)
		matched, _ := decodeErrResults[1].Interface().(bool)
		decErrI, _ := decodeErrResults[2].Interface().(error)
		if matched && decErrI == nil {
			err = ErrorPatternResponse{StatusCode: errResp.Status, Value: errResp.Value, Body: errResp.Body}
			return nil, err
		}
		err = UnexpectedStatusError{Method: method, Path: path, StatusCode: statusCode, Body: respBody, Header: resp.Header}
		return nil, err
	}

	// The route's OWN declaration (WithFormats) is the single source of
	// truth for which format applies — DecodeResponseWithFormats resolves
	// it; Client.Attach never duplicates that resolution logic itself.
	decodeRespMethod := handleVal.MethodByName("DecodeResponseWithFormats") // func([]byte, ...format.Format[Resp]) (Resp, error)
	decodeResults := decodeRespMethod.Call([]reflect.Value{reflect.ValueOf(respBody)})
	if errI, _ := decodeResults[1].Interface().(error); errI != nil {
		err = errI
		return nil, err
	}
	return decodeResults[0].Interface(), nil
}

var _ rest.ClientTransport = (*clientTransport)(nil)
