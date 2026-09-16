// Package error-types demonstrates every structured error type in go-codex.
//
// Each section triggers one specific error type, shows how to extract it via
// errors.As or errors.Is, accesses its typed fields, and logs it with slog so
// the structured LogValue() output is visible.
//
// Error types covered:
//   - codex.TypeMismatchError   — wrong Go type passed to a codec
//   - codex.ConstraintError     — Refine constraint check failed
//   - codex.ErrMissingField     — required struct field absent from input
//   - codex.ValidationError     — single field error from struct decode
//   - codex.ValidationErrors    — all field errors from struct decode
//   - codex.ElementError        — error at a specific slice index
//   - codex.KeyError            — error at a specific map key
//   - codex.UnknownVariantError — tagged-union discriminator has no matching codec
//   - codex.VariantError        — known variant's codec failed
//   - codex.EitherError         — all Either2 / UntaggedUnion branches failed
//   - rest.MiddlewareInputError  — a Transform-attached middleware's In fails to decode/validate
//   - rest.MiddlewareError       — a middleware fn's own business error, unmatched by any ErrorPattern
//   - rest.MiddlewareOutputError — a middleware's Out fails to encode into response headers/cookies
//
// See docs/guides/error-handling.md's "Middleware error paths — REST,
// events, reqreply side-by-side" section for the full cross-API picture —
// this example demonstrates REST in isolation since it needs no broker.
//
// Run with: go run ./examples/error-types
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

// logger writes structured text output to stdout for all sections.
var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
	// Omit timestamp so output is deterministic.
	ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	},
}))

// ── Shape types for TaggedUnion sections ─────────────────────────────────────

type Rectangle struct {
	Width  int
	Height int
}

type Square struct {
	Side int
}

type Shape struct {
	Rectangle *Rectangle
	Square    *Square
}

var rectangleCodec = codex.Struct[Rectangle](
	codex.RequiredField("width", codex.Int().Refine(validate.PositiveInt), func(r Rectangle) int { return r.Width }, func(r *Rectangle, v int) { r.Width = v }),
	codex.RequiredField("height", codex.Int().Refine(validate.PositiveInt), func(r Rectangle) int { return r.Height }, func(r *Rectangle, v int) { r.Height = v }),
)

var squareCodec = codex.Struct[Square](
	codex.RequiredField("side", codex.Int().Refine(validate.PositiveInt), func(s Square) int { return s.Side }, func(s *Square, v int) { s.Side = v }),
)

var shapeCodec = codex.TaggedUnion[Shape](
	"type",
	map[string]codex.Codec[Shape]{
		"rectangle": codex.MapCodecSafe(
			rectangleCodec,
			func(r Rectangle) Shape { return Shape{Rectangle: &r} },
			func(s Shape) (Rectangle, error) {
				if s.Rectangle == nil {
					return Rectangle{}, errors.New("shape is not a rectangle")
				}
				return *s.Rectangle, nil
			},
		),
		"square": codex.MapCodecSafe(
			squareCodec,
			func(s Square) Shape { return Shape{Square: &s} },
			func(sh Shape) (Square, error) {
				if sh.Square == nil {
					return Square{}, errors.New("shape is not a square")
				}
				return *sh.Square, nil
			},
		),
	},
	func(s Shape) (string, error) {
		switch {
		case s.Rectangle != nil:
			return "rectangle", nil
		case s.Square != nil:
			return "square", nil
		default:
			return "", errors.New("empty shape")
		}
	},
)

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// ── Section 1: TypeMismatchError ─────────────────────────────────────────
	//
	// Returned when a codec receives a value of an unexpected Go type.
	// All primitive codecs (Int, String, Bool, Bytes) and container codecs
	// (Struct, SliceOf, StringMap, TaggedUnion) return this when the input
	// is not the required type.

	fmt.Println("=== 1. TypeMismatchError ===")
	fmt.Println()

	_, err := codex.Int().Decode("thirty")
	var tme codex.TypeMismatchError
	if errors.As(err, &tme) {
		fmt.Printf("  expected=%q  got=%q\n", tme.Expected, tme.Got)
		logger.Error("type mismatch", slog.Any("error", tme))
	}
	fmt.Println()

	// ── Section 2: ConstraintError ───────────────────────────────────────────
	//
	// Returned directly from a Refine constraint when the check function
	// returns false. Name is the constraint identifier; Message describes the
	// specific failure for this value.

	fmt.Println("=== 2. ConstraintError ===")
	fmt.Println()

	_, err = codex.Int().Refine(validate.PositiveInt).Decode(-5)
	var ce codex.ConstraintError
	if errors.As(err, &ce) {
		fmt.Printf("  constraint=%q  message=%q\n", ce.Name, ce.Message)
		logger.Error("constraint failed", slog.Any("error", ce))
	}
	fmt.Println()

	// ── Section 3: ErrMissingField ───────────────────────────────────────────
	//
	// Sentinel error returned when a required struct field is absent from the
	// decoded map. The ValidationError wrapping it carries the field name;
	// errors.Is(fieldErr.Err, codex.ErrMissingField) identifies the cause.

	fmt.Println("=== 3. ErrMissingField ===")
	fmt.Println()

	type Req struct{ Name string }
	reqCodec := codex.Struct[Req](
		codex.RequiredField("name", codex.String(),
			func(r Req) string { return r.Name },
			func(r *Req, v string) { r.Name = v },
		),
	)

	_, err = reqCodec.Decode(map[string]any{})
	var ve codex.ValidationErrors
	if errors.As(err, &ve) {
		for _, fieldErr := range ve {
			if errors.Is(fieldErr.Err, codex.ErrMissingField) {
				fmt.Printf("  field=%q  missing=true\n", fieldErr.Field)
				logger.Error("missing required field", slog.Any("error", fieldErr))
			}
		}
	}
	fmt.Println()

	// ── Section 4: ValidationErrors + ValidationError ────────────────────────
	//
	// Struct decode collects all field failures into ValidationErrors before
	// returning. Each ValidationError carries the field name and its underlying
	// error (ConstraintError, ErrMissingField, TypeMismatchError, …).
	// Unwrap() returns []error so errors.Is / errors.As traverse the whole list.

	fmt.Println("=== 4. ValidationErrors + ValidationError ===")
	fmt.Println()

	type CreateUser struct {
		Name  string
		Email string
		Age   int
	}
	userCodec := codex.Struct[CreateUser](
		codex.RequiredField("name",
			codex.String().Refine(validate.NonEmptyString),
			func(r CreateUser) string { return r.Name },
			func(r *CreateUser, v string) { r.Name = v },
		),
		codex.RequiredField("email",
			codex.String().Refine(validate.Email),
			func(r CreateUser) string { return r.Email },
			func(r *CreateUser, v string) { r.Email = v },
		),
		codex.RequiredField("age",
			codex.Int().Refine(validate.PositiveInt),
			func(r CreateUser) int { return r.Age },
			func(r *CreateUser, v int) { r.Age = v },
		),
	)

	_, err = userCodec.Decode(map[string]any{"name": "", "email": "bad", "age": -1})
	if errors.As(err, &ve) {
		fmt.Printf("  %d field(s) failed:\n", len(ve))
		for _, fieldErr := range ve {
			var constraint codex.ConstraintError
			if errors.As(fieldErr.Err, &constraint) {
				fmt.Printf("    field=%q  constraint=%q  message=%q\n",
					fieldErr.Field, constraint.Name, constraint.Message)
			}
		}
		logger.Error("validation failed", slog.Any("errors", ve))
	}
	fmt.Println()

	// ── Section 5: ElementError ───────────────────────────────────────────────
	//
	// Returned by SliceOf when an element fails to decode. Index is the
	// zero-based position; Err is the underlying cause (here TypeMismatchError).

	fmt.Println("=== 5. ElementError ===")
	fmt.Println()

	sliceCodec := codex.SliceOf(codex.Int())
	_, err = sliceCodec.Decode([]any{10, "bad", 20})
	var ee codex.ElementError
	if errors.As(err, &ee) {
		fmt.Printf("  index=%d  cause=%v\n", ee.Index, ee.Err)
		logger.Error("slice element failed", slog.Any("error", ee))
	}
	fmt.Println()

	// ── Section 6: KeyError ───────────────────────────────────────────────────
	//
	// Returned by StringMap when a value fails to decode. Key is the map key;
	// Err is the underlying cause (here TypeMismatchError).

	fmt.Println("=== 6. KeyError ===")
	fmt.Println()

	mapCodec := codex.StringMap(codex.Int())
	_, err = mapCodec.Decode(map[string]any{"a": 5, "b": "not-an-int"})
	var ke codex.KeyError
	if errors.As(err, &ke) {
		fmt.Printf("  key=%q  cause=%v\n", ke.Key, ke.Err)
		logger.Error("map key failed", slog.Any("error", ke))
	}
	fmt.Println()

	// ── Section 7: UnknownVariantError ───────────────────────────────────────
	//
	// Returned by TaggedUnion when the discriminator field value does not match
	// any registered variant key. Tag is the discriminator field name; Variant
	// is the unrecognised value from the input. No Unwrap — this is terminal.

	fmt.Println("=== 7. UnknownVariantError ===")
	fmt.Println()

	_, err = shapeCodec.Decode(map[string]any{"type": "triangle", "sides": 3})
	var uve codex.UnknownVariantError
	if errors.As(err, &uve) {
		fmt.Printf("  tag=%q  variant=%q\n", uve.Tag, uve.Variant)
		logger.Error("unknown variant", slog.Any("error", uve))
	}
	fmt.Println()

	// ── Section 8: VariantError ───────────────────────────────────────────────
	//
	// Returned by TaggedUnion when a known variant is matched but its codec
	// fails. Tag and Variant identify which discriminator matched; Err is the
	// underlying failure (here ValidationErrors from the rectangle codec).
	// Unwrap() → Err, so errors.As can traverse into the inner error.

	fmt.Println("=== 8. VariantError ===")
	fmt.Println()

	_, err = shapeCodec.Decode(map[string]any{"type": "rectangle", "width": "oops", "height": 4})
	var varErr codex.VariantError
	if errors.As(err, &varErr) {
		fmt.Printf("  tag=%q  variant=%q  cause=%v\n", varErr.Tag, varErr.Variant, varErr.Err)
		logger.Error("variant decode failed", slog.Any("error", varErr))

		// The cause is ValidationErrors — errors.As traverses through VariantError.Unwrap.
		var innerVE codex.ValidationErrors
		if errors.As(varErr.Err, &innerVE) {
			fmt.Printf("  inner field errors: %d\n", len(innerVE))
			for _, fieldErr := range innerVE {
				fmt.Printf("    field=%q  err=%v\n", fieldErr.Field, fieldErr.Err)
			}
		}
	}
	fmt.Println()

	// ── Section 9: EitherError ────────────────────────────────────────────────
	//
	// Returned by Either2 and UntaggedUnion when ALL branches fail to decode.
	// Errors contains one error per branch in order. Unwrap() returns []error
	// so errors.Is / errors.As can traverse into any branch's error.

	fmt.Println("=== 9. EitherError ===")
	fmt.Println()

	// Either2[int, bool]: value "oops" is neither an int nor a bool.
	eitherCodec := codex.Either2(codex.Int(), codex.Bool())
	_, err = eitherCodec.Decode("oops")
	var eitherErr codex.EitherError
	if errors.As(err, &eitherErr) {
		fmt.Printf("  %d branch(es) failed:\n", len(eitherErr.Errors))
		for i, branchErr := range eitherErr.Errors {
			fmt.Printf("    branch %d: %v\n", i+1, branchErr)
		}
		logger.Error("either decode failed", slog.Any("error", eitherErr))

		// errors.Is / errors.As can traverse into individual branch errors.
		var tme codex.TypeMismatchError
		if errors.As(eitherErr, &tme) {
			fmt.Printf("  branch mismatch: expected=%q got=%q\n", tme.Expected, tme.Got)
		}
	}
	fmt.Println()

	// ── Section 10: Declarative middleware errors (api/rest) ────────────────
	//
	// A Transform-attached Middleware[In, Out] has THREE distinct failure
	// points — DecodeIn, Fn, EncodeOut — each with its own structured error
	// type. See docs/guides/error-handling.md's "Middleware error paths"
	// section for the full REST/events/reqreply side-by-side comparison;
	// this section demonstrates all 3 for REST in isolation (no broker
	// needed — runs entirely in-process via nethttp.ServeOne).

	fmt.Println("=== 10. Middleware errors (api/rest) ===")
	fmt.Println()

	runMiddlewareErrorDemo()
}

// ── Section 10 fixtures ───────────────────────────────────────────────────

type policyIn struct{ TenantID string }
type policyOut struct{ Ack string }

var policyInCodec = codex.Struct[policyIn](
	codex.RequiredField("tenant_id", codex.String().Refine(validate.NonEmptyString),
		func(in policyIn) string { return in.TenantID },
		func(in *policyIn, v string) { in.TenantID = v },
	),
)

var policyOutCodec = codex.Struct[policyOut](
	codex.RequiredField("ack", codex.String().Refine(validate.NonEmptyString),
		func(out policyOut) string { return out.Ack },
		func(out *policyOut, v string) { out.Ack = v },
	),
)

// policyEmptyIn has NO required fields — used for section 10c, isolating
// the demonstrated failure to EncodeOut (DecodeIn/InCodec.Validate always
// succeeds on a zero value).
type policyEmptyIn struct{}

var policyEmptyInCodec = codex.Struct[policyEmptyIn]()

type demoReq struct{ Name string }
type demoResp struct{ ID string }

var demoReqCodec = codex.Struct[demoReq](
	codex.RequiredField("name", codex.String(),
		func(r demoReq) string { return r.Name },
		func(r *demoReq, v string) { r.Name = v },
	),
)

var demoRespCodec = codex.Struct[demoResp](
	codex.RequiredField("id", codex.String(),
		func(r demoResp) string { return r.ID },
		func(r *demoResp, v string) { r.ID = v },
	),
)

// insufficientCreditError is the business error a middleware's Fn may
// return — declared as an ErrorPattern below so it's matched the SAME way
// a HANDLER error would be.
type insufficientCreditError struct{ Available int }

func (e insufficientCreditError) Error() string {
	return fmt.Sprintf("insufficient credit: available=%d", e.Available)
}

var creditErrorCodec = codex.Struct[insufficientCreditError](
	codex.RequiredField("available", codex.Int(),
		func(e insufficientCreditError) int { return e.Available },
		func(e *insufficientCreditError, v int) { e.Available = v },
	),
)

func runMiddlewareErrorDemo() {
	// ── 10a: DecodeIn failure → rest.MiddlewareInputError ──────────────
	//
	// The middleware requires "X-Tenant-Id"; the request omits it, so the
	// middleware's OWN InCodec validation fails before the handler ever runs.
	inMW := rest.NewMiddleware(middleware.NewDeclaration("tenant-policy", policyInCodec, policyOutCodec)).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Tenant-Id", codex.String(),
			func(in policyIn) string { return in.TenantID },
			func(in *policyIn, v string) { in.TenantID = v },
		))
	handlerCalled := false
	inRoute := rest.NewRoute[demoReq, demoResp]("POST", "/orders", demoReqCodec, demoRespCodec,
		rest.RouteMeta{OperationID: "createOrderIn"},
	)
	inRoute = rest.Transform(inRoute, inMW, func(ctx context.Context, req *demoReq, in policyIn) (policyOut, error) {
		return policyOut{Ack: "ok"}, nil
	})
	inRoute = inRoute.WithHandler(func(_ context.Context, req demoReq) (demoResp, error) {
		handlerCalled = true
		return demoResp{ID: "1"}, nil
	})
	inHandler, err := nethttp.ServeOne(inRoute)
	if err != nil {
		logger.Error("ServeOne (10a)", "error", err)
		return
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"widget"}`))
	r.Header.Set("Content-Type", "application/json")
	// X-Tenant-Id deliberately omitted.
	inHandler.ServeHTTP(rec, r)
	fmt.Printf("  10a DecodeIn failure: status=%d handlerCalled=%v (want false — middleware failure short-circuits)\n", rec.Code, handlerCalled)
	logger.Error("middleware DecodeIn failed", "status", rec.Code, "body", rec.Body.String())
	fmt.Println()

	// ── 10b: Fn business error → matched by a declared ErrorPattern ─────
	//
	// The SAME ErrorPattern mechanism a HANDLER error uses also matches a
	// middleware Fn's own business error — declared once, catches both.
	fnMW := rest.NewMiddleware(middleware.NewDeclaration("credit-policy", policyInCodec, policyOutCodec)).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Tenant-Id", codex.String(),
			func(in policyIn) string { return in.TenantID },
			func(in *policyIn, v string) { in.TenantID = v },
		))
	fnRoute := rest.NewRoute[demoReq, demoResp]("POST", "/orders", demoReqCodec, demoRespCodec,
		rest.RouteMeta{OperationID: "createOrderFn"},
		rest.ErrorPattern[insufficientCreditError, insufficientCreditError](http.StatusPaymentRequired, creditErrorCodec),
	)
	fnRoute = rest.Transform(fnRoute, fnMW, func(ctx context.Context, req *demoReq, in policyIn) (policyOut, error) {
		return policyOut{}, insufficientCreditError{Available: 5}
	})
	fnRoute = fnRoute.WithHandler(func(_ context.Context, req demoReq) (demoResp, error) {
		return demoResp{ID: "1"}, nil
	})
	fnHandler, err := nethttp.ServeOne(fnRoute)
	if err != nil {
		logger.Error("ServeOne (10b)", "error", err)
		return
	}
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"widget"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tenant-Id", "acme")
	fnHandler.ServeHTTP(rec, r)
	fmt.Printf("  10b Fn business error (ErrorPattern-matched): status=%d body=%s\n", rec.Code, rec.Body.String())
	fmt.Println()

	// ── 10c: EncodeOut failure → rest.MiddlewareOutputError ─────────────
	//
	// The middleware's Fn succeeds, but its returned Out fails its OWN
	// OutCodec validation while being encoded into the response — a
	// DIFFERENT failure point from 10a/10b, reported with its own
	// observer location ("middleware:out") and its own error type.
	outMW := rest.NewMiddleware(middleware.NewDeclaration("ack-policy", policyEmptyInCodec, policyOutCodec)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Ack", codex.String(),
			func(out policyOut) string { return out.Ack },
			func(out *policyOut, v string) { out.Ack = v },
		))
	outRoute := rest.NewRoute[demoReq, demoResp]("POST", "/orders", demoReqCodec, demoRespCodec,
		rest.RouteMeta{OperationID: "createOrderOut"},
	)
	outRoute = rest.Transform(outRoute, outMW, func(ctx context.Context, req *demoReq, in policyEmptyIn) (policyOut, error) {
		// Empty Ack fails policyOutCodec's NonEmptyString refinement at
		// EncodeOut/OutCodec.Validate time — NOT the fn itself.
		return policyOut{Ack: ""}, nil
	})
	outRoute = outRoute.WithHandler(func(_ context.Context, req demoReq) (demoResp, error) {
		return demoResp{ID: "1"}, nil
	})
	outHandler, err := nethttp.ServeOne(outRoute)
	if err != nil {
		logger.Error("ServeOne (10c)", "error", err)
		return
	}
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"widget"}`))
	r.Header.Set("Content-Type", "application/json")
	outHandler.ServeHTTP(rec, r)
	fmt.Printf("  10c EncodeOut failure: status=%d body=%s\n", rec.Code, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "ack-policy") {
		logger.Warn("expected the response body to embed the failing middleware's Name via MiddlewareOutputError")
	}
	fmt.Println()
}
