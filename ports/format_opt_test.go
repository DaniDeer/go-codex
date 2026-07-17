// Package ports_test — regression tests confirming the api/rest, api/events,
// and api/reqreply format RouteOpt/ChannelOpt constructors (RequestFormats,
// Formats, SubscribeFormats, PublishFormats) work through ports.Pattern.Opts
// with ZERO changes needed in the ports package — the whole point of the
// symmetry: Opts is already []rest.RouteOpt / []events.ChannelOpt /
// []reqreply.RouteOpt, and the new option constructors just implement those
// existing interfaces.
package ports_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/validate"
)

func TestRESTPattern_FormatOptZeroPortsChanges(t *testing.T) {
	png := codex.Bytes().Refine(validate.PNG)
	p, err := ports.NewIOPort[int, []byte]("img-io", intCodec, png, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "GET", Path: "/images/{id}",
				Opts: []rest.RouteOpt{
					rest.Formats(format.Binary(png).WithContentType("image/png")),
				}},
		},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, ok := ports.RESTHandle[int, []byte](p)
	if !ok {
		t.Fatal("want handle")
	}
	if len(handle.Formats) != 1 || handle.Formats[0].ContentType() != "image/png" {
		t.Errorf("want Formats via Pattern.Opts, got %+v", handle.Formats)
	}
}

func TestEventPattern_FormatOptZeroPortsChanges(t *testing.T) {
	png := codex.Bytes().Refine(validate.PNG)
	p, err := ports.NewSinkPort[[]byte]("img-sink", png, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.EventPattern{Topic: "images/{id}",
				Opts: []events.ChannelOpt{
					events.PublishFormats(format.Binary(png).WithContentType("image/png")),
				}},
		},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, ok := ports.EventHandle[[]byte](p)
	if !ok {
		t.Fatal("want handle")
	}
	if len(handle.PublishFormats) != 1 || handle.PublishFormats[0].ContentType() != "image/png" {
		t.Errorf("want PublishFormats via Pattern.Opts, got %+v", handle.PublishFormats)
	}
}

func TestReqReplyPattern_FormatOptZeroPortsChanges(t *testing.T) {
	png := codex.Bytes().Refine(validate.PNG)
	p, err := ports.NewIOPort[int, []byte]("img-rr", intCodec, png, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.ReqReplyPattern{Topic: "images/get",
				Opts: []reqreply.RouteOpt{
					reqreply.Formats(format.Binary(png).WithContentType("image/png")),
				}},
		},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, ok := ports.ReqReplyHandle[int, []byte](p)
	if !ok {
		t.Fatal("want handle")
	}
	if len(handle.Formats) != 1 || handle.Formats[0].ContentType() != "image/png" {
		t.Errorf("want Formats via Pattern.Opts, got %+v", handle.Formats)
	}
}
