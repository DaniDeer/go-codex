// Package v3 renders schema.Schema values and route.SecurityScheme definitions
// as an AsyncAPI 3.0 document.
//
// It is the 3.0 counterpart to render/asyncapi/v2 (which remains frozen at 2.6).
// The structural changes from 2.6 to 3.0 include:
//
//   - Version string changes to "3.0.0".
//   - Channels and operations are separated: channels describe topics and their
//     messages; a top-level operations map links operations to channels via $ref.
//   - Channel keys are logical identifiers; the actual topic address is in
//     ChannelItem.Address.
//   - Subscribe/publish are replaced by action: "receive" / "send".
//   - Per-operation security is supported in addition to server-level security.
//
// Typical usage:
//
//	doc, err := v3.NewDocumentBuilder(v3.Info{
//	    Title:   "User Events",
//	    Version: "1.0.0",
//	}).
//	    AddServer("production", v3.Server{
//	        URL:      "broker.example.com",
//	        Protocol: "mqtt",
//	    }).
//	    AddSecurityScheme("bearerAuth", route.BearerScheme("JWT")).
//	    AddChannel("userCreated", v3.ChannelItem{
//	        Address: "user/created",
//	        Subscribe: &v3.Operation{
//	            Summary:  "User created event",
//	            Security: []route.SecurityRequirement{route.Require("bearerAuth")},
//	            Message:  v3.Message{Schema: UserCodec.Schema, SchemaName: "User"},
//	        },
//	    }).
//	    Build()
//
//	yamlBytes, err := doc.MarshalYAML()
package v3
