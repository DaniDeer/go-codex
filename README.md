# GO Codex

## Project Structure

```TEXT
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC CORE API
│   ├── codec.go           # Codec[T]
│   ├── map.go             # MapCodecSafe
│   ├── refine.go          # Constraint + Refine
│
├── primitive/              # basic codecs
│   ├── string.go
│   ├── int.go
│
├── object/                 # struct composition
│   ├── struct.go
│   ├── field.go
│
├── union/                  # tagged unions
│   ├── tagged.go
│
├── schema/                 # schema model
│   ├── schema.go
│
├── validate/               # reusable constraints
│   ├── number.go
│   ├── string.go
│
└── examples/
    └── shape/
        └── main.go
```
