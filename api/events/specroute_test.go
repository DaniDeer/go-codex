package events_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

type specTestReading struct{ Value int }

var specTestReadingCodec = codex.Struct[specTestReading](
	codex.RequiredField("value", codex.Int(),
		func(r specTestReading) int { return r.Value },
		func(r *specTestReading, v int) { r.Value = v },
	),
)

var sensorReadingsChannel = events.NewChannel[specTestReading]("sensor/readings", specTestReadingCodec)

// recordingPublishTransport is a minimal events.Transport that records
// every Publish call's arguments — letting a test inspect exactly what
// [Client.ServeSpec] published (topic/content), without any real network
// transport.
type recordingPublishTransport struct {
	calls []recordingPublishCall
}

type recordingPublishCall struct {
	pub any
	msg any
}

func (t *recordingPublishTransport) Publish(_ context.Context, pub, msg any, _ ...events.ClientPublishOptions) error {
	t.calls = append(t.calls, recordingPublishCall{pub: pub, msg: msg})
	return nil
}
func (t *recordingPublishTransport) Subscribe(context.Context, any, any, ...events.ClientSubscribeOptions) error {
	return nil
}
func (t *recordingPublishTransport) ServeSubscribers(context.Context) error { return nil }

func specTestClient(t *testing.T) (*events.Client, *recordingPublishTransport) {
	t.Helper()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test API", Version: "1.0.0"}))
	_, err := sensorReadingsChannel.WithPublish(events.Publish{OperationID: "publishReading"}).Handle(c)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	ft := &recordingPublishTransport{}
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return c, ft
}

func TestServeSpec_HappyPath_DefaultYAML(t *testing.T) {
	c, ft := specTestClient(t)

	if err := c.ServeSpec(context.Background(), "spec/yaml"); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}
	if len(ft.calls) != 1 {
		t.Fatalf("want exactly 1 Publish call, got %d", len(ft.calls))
	}
	body, ok := ft.calls[0].msg.([]byte)
	if !ok {
		t.Fatalf("published msg has unexpected type %T", ft.calls[0].msg)
	}
	if len(body) == 0 {
		t.Error("published YAML body is empty")
	}
	if !strings.Contains(string(body), "sensor") {
		t.Error("published YAML body does not reference the registered channel")
	}

	handle, ok := ft.calls[0].pub.(*events.ChannelHandle[[]byte])
	if !ok {
		t.Fatalf("pub has unexpected type %T", ft.calls[0].pub)
	}
	if len(handle.Formats) != 1 {
		t.Fatalf("want 1 Format (format.Binary) declared so a subscriber can decode the raw payload, got %d", len(handle.Formats))
	}
}

func TestServeSpec_JSONFormat(t *testing.T) {
	c, ft := specTestClient(t)

	if err := c.ServeSpec(context.Background(), "spec/json", events.WithSpecFormat(events.SpecFormatJSON)); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}
	if len(ft.calls) != 1 {
		t.Fatalf("want exactly 1 Publish call, got %d", len(ft.calls))
	}
	body, ok := ft.calls[0].msg.([]byte)
	if !ok {
		t.Fatalf("published msg has unexpected type %T", ft.calls[0].msg)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		t.Errorf("JSON body does not look like JSON: %s", body)
	}
}

func TestServeSpec_CalledTwice_PublishesTwice(t *testing.T) {
	c, ft := specTestClient(t)

	if err := c.ServeSpec(context.Background(), "spec/yaml"); err != nil {
		t.Fatalf("first ServeSpec: %v", err)
	}
	if err := c.ServeSpec(context.Background(), "spec/yaml"); err != nil {
		t.Fatalf("second ServeSpec: %v", err)
	}
	if len(ft.calls) != 2 {
		t.Fatalf("want 2 Publish calls (ServeSpec always republishes), got %d", len(ft.calls))
	}
}

func TestServeSpec_EmptyTopic_Error(t *testing.T) {
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test API", Version: "1.0.0"}))
	err := c.ServeSpec(context.Background(), "")
	if err == nil {
		t.Fatal("want error for empty topic, got nil")
	}
	var topicErr events.SpecTopicRequiredError
	if !errors.As(err, &topicErr) {
		t.Fatalf("want SpecTopicRequiredError, got %T: %v", err, err)
	}
	if got := topicErr.LogValue().Kind().String(); got != "Group" {
		t.Errorf("LogValue kind = %q, want Group", got)
	}
}

func TestServeSpec_WithSpecMiddleware(t *testing.T) {
	c, ft := specTestClient(t)
	marker := func() {}
	if err := c.ServeSpec(context.Background(), "spec/yaml", events.WithSpecMiddleware(nil, marker)); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}
	if len(ft.calls) != 1 {
		t.Fatalf("want exactly 1 Publish call, got %d", len(ft.calls))
	}
	handle, ok := ft.calls[0].pub.(*events.ChannelHandle[[]byte])
	if !ok {
		t.Fatalf("pub has unexpected type %T", ft.calls[0].pub)
	}
	if len(handle.ClientImplementations) != 1 {
		t.Fatalf("want 1 ClientImplementation, got %d", len(handle.ClientImplementations))
	}
	if handle.ClientImplementations[0].Name != "fulfill:general#0" {
		t.Errorf("want fulfill:general#0, got %q", handle.ClientImplementations[0].Name)
	}
}

func TestServeSpec_NoTransportAttached_Error(t *testing.T) {
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test API", Version: "1.0.0"}))
	err := c.ServeSpec(context.Background(), "spec/yaml")
	var noTransportErr events.NoTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoTransportAttachedError, got %T: %v", err, err)
	}
}

// ExampleClient_ServeSpec demonstrates publishing a self-serving AsyncAPI
// spec — pub/sub has no format-negotiation mechanism at publish time, so
// a caller wanting both YAML and JSON calls ServeSpec twice, with two
// different topics.
func ExampleClient_ServeSpec() {
	c := events.NewClient(events.WithInfo(events.Info{Title: "Sensor API", Version: "1.0.0"}))
	if _, err := sensorReadingsChannel.WithPublish(events.Publish{OperationID: "publishReading"}).Handle(c); err != nil {
		fmt.Println("handle error:", err)
		return
	}
	if err := c.Attach(&recordingPublishTransport{}); err != nil {
		fmt.Println("attach error:", err)
		return
	}

	if err := c.ServeSpec(context.Background(), "spec/yaml"); err != nil {
		fmt.Println("ServeSpec error:", err)
		return
	}
	if err := c.ServeSpec(context.Background(), "spec/json", events.WithSpecFormat(events.SpecFormatJSON)); err != nil {
		fmt.Println("ServeSpec error:", err)
		return
	}

	fmt.Println("spec published in both formats")
	// Output:
	// spec published in both formats
}
