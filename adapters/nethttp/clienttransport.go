package nethttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
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
// security/credential handling, no per-call format override, no
// error-pattern decoding). Per Decision 6 (see
// docs/roadmap/pubsub-workflow-simplification.md), [Attach] is the sole
// public entry point built on the now-unexported `call` primitive; a
// caller needing the omitted features (or a pre-built *rest.RouteHandle,
// e.g. adapters/mcprest bridging a REST route into another protocol)
// uses the still-public [CallWithHandle] directly instead.
//
//	client := rest.NewClient()
//	if err := nethttp.Attach(client, httpClient, baseURL); err != nil { ... }
//	respAny, err := client.Call(ctx, getUserRoute, GetUserReq{ID: "f47ac10b"})
//	resp := respAny.(GetUserResp)
func Attach(client *rest.Client, httpClient *http.Client, baseURL string) error {
	return client.Attach(&clientTransport{caller: newCaller(httpClient, baseURL)})
}

// Call implements [rest.ClientTransport]. See [Attach]'s doc comment for
// v1 scope notes.
func (t *clientTransport) Call(ctx context.Context, routeAny, reqAny any) (any, error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() || rv.Type().PkgPath() != restPkgPath || !strings.HasPrefix(rv.Type().Name(), "Route[") {
		return nil, rest.TransportTypeMismatchError{Want: "rest.Route[Req, Resp]", Got: fmt.Sprintf("%T", routeAny)}
	}
	handleResults := rv.MethodByName("ClientHandle").Call(nil)
	handleVal := handleResults[0]
	elem := handleVal.Elem()

	descriptor := elem.FieldByName("Descriptor")
	method := strings.ToUpper(descriptor.FieldByName("Method").String())
	path := descriptor.FieldByName("Path").String()

	encodeReqField := elem.FieldByName("EncodeRequest") // func(Req) ([]byte, error)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != encodeReqField.Type().In(0) {
		return nil, rest.TransportTypeMismatchError{
			Path: path, Want: encodeReqField.Type().In(0).String(), Got: fmt.Sprintf("%T", reqAny),
		}
	}

	buildPathResults := handleVal.MethodByName("BuildPath").Call([]reflect.Value{reflect.ValueOf(map[string]string(nil))})
	if errI, _ := buildPathResults[1].Interface().(error); errI != nil {
		return nil, errI
	}
	concretePath, _ := buildPathResults[0].Interface().(string)

	var body io.Reader
	var contentType string
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		encodeResults := encodeReqField.Call([]reflect.Value{reqVal})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			return nil, fmt.Errorf("nethttp: encode request: %w", errI)
		}
		bodyBytes, _ := encodeResults[0].Interface().([]byte)
		body = bytes.NewReader(bodyBytes)
		contentType = "application/json"
	}

	rawURL := strings.TrimRight(t.caller.baseURL, "/") + concretePath
	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, RequestBuildError{Err: err}
	}
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := t.caller.client.Do(httpReq)
	if err != nil {
		return nil, RequestError{Method: method, Path: path, Err: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ResponseBodyError{Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, UnexpectedStatusError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: respBody}
	}

	decodeRespField := elem.FieldByName("DecodeResponse") // func([]byte) (Resp, error)
	decodeResults := decodeRespField.Call([]reflect.Value{reflect.ValueOf(respBody)})
	if errI, _ := decodeResults[1].Interface().(error); errI != nil {
		return nil, errI
	}
	return decodeResults[0].Interface(), nil
}

var _ rest.ClientTransport = (*clientTransport)(nil)
