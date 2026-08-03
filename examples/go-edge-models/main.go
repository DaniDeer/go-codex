// Resources
// - examples/flat-key-patch -> demonstrates dotted-key JSON patching with go-codex
package main

import (
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/format"
)

// ── Key constants ─────────────────────────────────────────────────────────────

const (
// moduleKeyPrefix is the fixed namespace for all module keys.
//moduleKeyPrefix = "properties.desired.modules."
)

func main() {
	// A real IoT-Edge module "settings" document: "createOptions" is a
	// JSON-escaped STRING on the wire (Docker's own create-options shape),
	// decoded transparently into the fully-typed CreateOptions struct via
	// format.EmbeddedJSON(CreateOptionsCodec) wired into ModuleSettingsCodec.
	settingsJSON := `{
		"image": "traefik:v2.10",
		"createOptions": "{\"ExposedPorts\":{\"8080/tcp\":{},\"8443/tcp\":{},\"8090/tcp\":{}},\"HostConfig\":{\"Binds\":[\"/etc/ssl/local/pubkey.pem:/etc/traefik/ssl/pubkey.pem\",\"/etc/ssl/local/private.key:/etc/traefik/ssl/private.key\"],\"PortBindings\":{\"8080/tcp\":[{\"HostPort\":\"8090\"}],\"8443/tcp\":[{\"HostPort\":\"9090\"}],\"8090/tcp\":[{\"HostPort\":\"9092\"}]}}}"
	}`

	jSettings := format.JSON(ModuleSettingsCodec)
	settings, err := jSettings.Unmarshal([]byte(settingsJSON))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("ModuleSettings.Image:", settings.Image)
	fmt.Println("CreateOptions.ExposedPorts:", settings.CreateOptions.ExposedPorts)
	fmt.Printf("CreateOptions.HostConfig.Binds: %+v\n", settings.CreateOptions.HostConfig.Binds)
	fmt.Printf("CreateOptions.HostConfig.PortBindings: %+v\n", settings.CreateOptions.HostConfig.PortBindings)

	// Round trip: re-encode back to the same wire shape (createOptions is
	// re-escaped as a JSON string automatically).
	reEncoded, err := jSettings.Marshal(settings)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("re-encoded:", string(reEncoded))
}
