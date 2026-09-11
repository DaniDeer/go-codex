package reqreply

import asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"

// Info is an alias for [asyncapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = asyncapi.Info

// ServerEntry is an alias for [asyncapi.Server] — an AsyncAPI server entry
// (e.g. {URL: "mqtt://broker:1883", Protocol: "mqtt5"}) registered via
// [Server.AddServer]. Named ServerEntry (not Server) to avoid colliding
// with [Server], the dispatch-owning type that accumulates route
// registrations and produces the AsyncAPI document — mirrors
// [rest.ServerEntry]/[rest.Server]'s identical naming split exactly (a
// gap [api/events] never hit, since [events.Client] handles both roles
// without a separate "Server" dispatch type).
type ServerEntry = asyncapi.Server
