package zeromq

import asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"

// Info is an alias for [asyncapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = asyncapi.Info

// Server is an alias for [asyncapi.Server].
type Server = asyncapi.Server

// SocketMeta holds per-socket metadata for the AsyncAPI 3.0 spec generated
// by [Builder].
//
// All fields are optional. When OperationID is empty, operation IDs are
// derived from the route path.
type SocketMeta struct {
	// OperationID is the base name for the two generated operations.
	// The send operation is named "<OperationID>" and the receive operation
	// is named "<OperationID>Reply". When empty, the route path is used as
	// the base (e.g. path "/compute" → "sendCompute" / "receiveComputeReply").
	OperationID string

	// Summary is a short human-readable summary of the socket contract.
	// Appears in both operations in the AsyncAPI spec.
	Summary string

	// Description is a longer human-readable description.
	// Appears in the send operation.
	Description string

	// Tags attach arbitrary labels to the operations in the AsyncAPI spec.
	Tags []string

	// ReqSchemaName, when non-empty, registers the request payload schema in
	// components/schemas under this name and emits a $ref instead of inlining
	// the schema. Use this to share request schemas across multiple sockets.
	ReqSchemaName string

	// RespSchemaName, when non-empty, registers the response payload schema in
	// components/schemas and emits a $ref. Use when the same response type
	// appears in multiple sockets.
	RespSchemaName string
}
