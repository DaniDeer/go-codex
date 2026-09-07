package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// ── Layer 2: pure domain functions ───────────────────────────────────────────
//
// Zero IO — no database, no HTTP, no external services. Unit-testable with
// plain Go structs and no setup.

// BuildUserRecord creates a database record from a user creation request.
func BuildUserRecord(req routes.CreateUserReq) routes.UserRecord {
	return routes.UserRecord{
		ID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Name:  req.Name,
		Email: req.Email,
	}
}

// BuildUserResponse projects a database record into the HTTP response entity.
func BuildUserResponse(record routes.UserRecord) routes.User {
	return routes.User(record)
}

// ── Layer 3: infrastructure (business logic, no adapter-specific IO) ────────

// WithDomainLogging is a decorator that wraps a handler function, logging
// success (Info) or failure (Error) after the handler returns — reused
// identically by every route's handler, on either adapter.
func WithDomainLogging[Req, Resp any](
	name string,
	handler func(context.Context, Req) (Resp, error),
	logger *slog.Logger,
	extractAttrs func(Req, Resp) []slog.Attr,
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			logger.ErrorContext(ctx, name+" failed", "error", err)
		} else {
			attrs := extractAttrs(req, resp)
			args := make([]any, 0, len(attrs)*2)
			for _, attr := range attrs {
				args = append(args, attr.Key, attr.Value.Any())
			}
			logger.InfoContext(ctx, name+" succeeded", args...)
		}
		return resp, err
	}
}

// MakeCreateUserHandler orchestrates the create-user pipeline:
//
// decode (codec) → BuildUserRecord (L2) → Save (store IO) → BuildUserResponse (L2) → encode (codec)
//
// The Location response header and session response cookie (VALUE and
// Set-Cookie ATTRIBUTES) are now 100% declarative on routes.CreateUserRoute
// itself — derived
// directly from the returned User by the adapter, AFTER this handler
// returns. No adapter-specific escape hatch (the former ResponseDepositor)
// is needed here at all.
func MakeCreateUserHandler(store *UserStore) func(context.Context, routes.CreateUserReq) (routes.User, error) {
	return func(ctx context.Context, req routes.CreateUserReq) (routes.User, error) {
		record := BuildUserRecord(req)
		if err := store.Save(record); err != nil {
			return routes.User{}, err
		}
		return BuildUserResponse(record), nil
	}
}

// MakeGetUserHandler orchestrates the get-user pipeline. GetUserReq.ID
// arrives ALREADY merged and codec-validated by the adapter's server
// dispatch (via rest.NewPathParam + RouteHandle.DecodeMerged) — no manual
// path-var extraction needed here, on EITHER adapter.
func MakeGetUserHandler(store *UserStore) func(context.Context, routes.GetUserReq) (routes.User, error) {
	return func(_ context.Context, req routes.GetUserReq) (routes.User, error) {
		record, ok := store.Get(req.ID.String())
		if !ok {
			return routes.User{}, fmt.Errorf("user %q not found", req.ID)
		}
		return BuildUserResponse(record), nil
	}
}

// MakeUpdateUserHandler orchestrates the update-user pipeline:
//
// req.ID (path, merged) + req.Name/req.Email (body, decoded) → Save (store IO) → BuildUserResponse
func MakeUpdateUserHandler(store *UserStore) func(context.Context, routes.UpdateUserReq) (routes.User, error) {
	return func(_ context.Context, req routes.UpdateUserReq) (routes.User, error) {
		record := routes.UserRecord(req)
		if err := store.Save(record); err != nil {
			return routes.User{}, err
		}
		return BuildUserResponse(record), nil
	}
}

// MakeListUsersHandler handles GET /users. req.Page/req.Search arrive
// ALREADY merged from the query string — no manual query parsing needed.
func MakeListUsersHandler() func(context.Context, routes.ListUsersReq) (routes.PagedUsersResp, error) {
	return func(_ context.Context, req routes.ListUsersReq) (routes.PagedUsersResp, error) {
		return routes.PagedUsersResp{Page: req.Page, Search: req.Search, Users: nil}, nil
	}
}

// MakeProfileHandler handles GET /profile. req.SessionToken/req.RequestID
// arrive ALREADY merged and validated from the cookie/header — the
// handler trusts them completely.
func MakeProfileHandler() func(context.Context, routes.ProfileReq) (routes.User, error) {
	return func(_ context.Context, _ routes.ProfileReq) (routes.User, error) {
		return routes.User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Alice", Email: "alice@example.com"}, nil
	}
}

// invalidCredentialsError is returned by MakeLoginHandler when the
// username or password is wrong. Each server's ErrorHandler maps it to
// 401 Unauthorized.
type invalidCredentialsError struct{ Err error }

func (e invalidCredentialsError) Error() string { return e.Err.Error() }
func (e invalidCredentialsError) Unwrap() error { return e.Err }

// InvalidCredentialsError exposes invalidCredentialsError for
// chiserver/nethttpserver's ErrorHandler to errors.As against.
type InvalidCredentialsError = invalidCredentialsError

// MakeLoginHandler issues a mock bearer token for a known username/password
// pair — "alice"/"secret" gets a profile-scoped token, "admin"/"secret"
// gets a profile+admin-scoped token.
func MakeLoginHandler() func(context.Context, routes.LoginReq) (routes.TokenResp, error) {
	return func(_ context.Context, req routes.LoginReq) (routes.TokenResp, error) {
		switch {
		case req.Username == "alice" && req.Password == "secret":
			return routes.TokenResp{Token: "valid-user-token"}, nil
		case req.Username == "admin" && req.Password == "secret":
			return routes.TokenResp{Token: "valid-admin-token"}, nil
		default:
			return routes.TokenResp{}, invalidCredentialsError{Err: fmt.Errorf("invalid credentials")}
		}
	}
}

// MakeAdminActionHandler performs a mock privileged action.
func MakeAdminActionHandler() func(context.Context, routes.AdminActionReq) (routes.AdminActionResp, error) {
	return func(_ context.Context, req routes.AdminActionReq) (routes.AdminActionResp, error) {
		return routes.AdminActionResp{Result: "action " + req.Action + " executed"}, nil
	}
}
