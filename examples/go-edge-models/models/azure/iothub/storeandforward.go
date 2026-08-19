package iothub

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── StoreAndForwardConfiguration ───────────────────────────────────────────────
//
// Wire: {"timeToLiveSecs": 259200}
//
// $edgeHub's message store-and-forward retention window — how long
// undelivered messages are retained before being dropped.
type StoreAndForwardConfiguration struct {
	TimeToLiveSecs int64
}

var StoreAndForwardConfigurationCodec = c.Struct[StoreAndForwardConfiguration](
	c.RequiredField("timeToLiveSecs", c.Int64().Refine(v.PositiveInt64),
		func(sf StoreAndForwardConfiguration) int64 { return sf.TimeToLiveSecs },
		func(sf *StoreAndForwardConfiguration, val int64) { sf.TimeToLiveSecs = val },
	),
)
