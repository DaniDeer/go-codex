package registry

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
)

// PingRoute is the Docker Registry HTTP API v2 base check, GET /v2/. A 200
// response means the registry is reachable and does not require auth for
// this request; a 401 response carries a WWW-Authenticate challenge header
// (see auth.go's authenticate). Response body is always empty.
//
// Ping has no dedicated client function (unlike GetTags/GetImageMetadata)
// — it is used internally by auth.go's authenticate() as the first step
// of the Bearer-token challenge flow, and has no LLM-facing value of its
// own, so it also has no MCP tool. It remains directly callable via
// PingRoute.ClientHandle() for advanced/low-level use (e.g. a health
// check independent of any auth flow).
var PingRoute = rest.NewRoute[struct{}, struct{}](
	"GET", "/v2/",
	c.Empty, c.Empty,
	rest.RouteMeta{
		OperationID: "ping",
		Summary:     "Docker Registry HTTP API v2 base check",
	},
)
