package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

type ev struct {
	ID string
}

var evCodec = codex.Struct[ev](
	codex.RequiredField("id", codex.String(), func(e ev) string { return e.ID }, func(e *ev, v string) { e.ID = v }),
)

type fakeToken struct{}

func (fakeToken) Wait() bool                 { return true }
func (fakeToken) WaitTimeout(d int64) bool   { return true }
func (fakeToken) Done() <-chan struct{}      { c := make(chan struct{}); close(c); return c }
func (fakeToken) Error() error               { return nil }

func main() {
	uuidCodec := codex.String().Refine(validate.UUID)
	pub := events.NewChannel[ev]("things/{id}/x", evCodec,
		events.NewTopicParam("id", uuidCodec,
			func(e ev) string { return e.ID },
			func(e *ev, v string) { e.ID = v }),
	).WithPublish(events.Publish{})

	transport := mqtt.NewPublishTransport[ev](nil, 1, false, mqtt.PublishOptions[ev]{})
	err := events.PublishHandle(context.Background(), pub, transport, ev{ID: "not-a-uuid"})
	fmt.Printf("%T: %v\n", err, err)
	_ = pahomqtt.Client(nil)
}
