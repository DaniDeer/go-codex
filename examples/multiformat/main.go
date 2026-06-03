package main

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// Config is a simple application configuration struct.
type Config struct {
	Host    string
	Port    int
	Debug   bool
	Timeout float64
}

// configCodec is defined once and works with every format.
var configCodec = codex.Struct[Config](
	codex.RequiredField("host", codex.String().Refine(validate.NonEmptyString), func(c Config) string { return c.Host }, func(c *Config, v string) { c.Host = v }),
	codex.RequiredField("port", codex.Int().Refine(validate.RangeInt(1, 65535)), func(c Config) int { return c.Port }, func(c *Config, v int) { c.Port = v }),
	codex.OptionalField("debug", codex.Bool(), func(c Config) bool { return c.Debug }, func(c *Config, v bool) { c.Debug = v }),
	codex.RequiredField("timeout", codex.Float64().Refine(validate.PositiveFloat), func(c Config) float64 { return c.Timeout }, func(c *Config, v float64) { c.Timeout = v }),
)

var (
	jsonFmt = format.JSON(configCodec)
	yamlFmt = format.YAML(configCodec)
	tomlFmt = format.TOML(configCodec)
)

func main() {
	jsonData := []byte(`{"host":"localhost","port":8080,"debug":true,"timeout":30.5}`)

	yamlData := []byte(`
host: localhost
port: 8080
debug: true
timeout: 30.5
`)

	tomlData := []byte(`
host = "localhost"
port = 8080
debug = true
timeout = 30.5
`)

	// Decode the same config from three different formats.
	fromJSON, err := jsonFmt.Unmarshal(jsonData)
	check("JSON", err)

	fromYAML, err := yamlFmt.Unmarshal(yamlData)
	check("YAML", err)

	fromTOML, err := tomlFmt.Unmarshal(tomlData)
	check("TOML", err)

	fmt.Printf("from JSON: %+v\n", fromJSON)
	fmt.Printf("from YAML: %+v\n", fromYAML)
	fmt.Printf("from TOML: %+v\n", fromTOML)

	// Encode one Go value to all three formats using the same codec.
	cfg := Config{Host: "api.example.com", Port: 443, Debug: false, Timeout: -10.0}
	fmt.Println()

	jsonOut, _ := jsonFmt.Marshal(cfg)
	yamlOut, _ := yamlFmt.Marshal(cfg)
	tomlOut, _ := tomlFmt.Marshal(cfg)

	fmt.Printf("JSON:\n%s\n", jsonOut)
	fmt.Printf("YAML:\n%s\n", yamlOut)
	fmt.Printf("TOML:\n%s\n", tomlOut)

	// Validation works the same regardless of format.
	fmt.Println("validation errors:")
	badJSON := []byte(`{"host":"","port":99999,"debug":false,"timeout":30.0}`)
	_, err = jsonFmt.Unmarshal(badJSON)
	fmt.Println(" JSON:", err)

	badTOML := []byte("host = \"x\"\nport = -1\ntimeout = 5.0\n")
	_, err = tomlFmt.Unmarshal(badTOML)
	fmt.Println(" TOML:", err)
}

func check(label string, err error) {
	if err != nil {
		fmt.Printf("%s error: %v\n", label, err)
	}
}
