package docker

import (
	"fmt"
	"strconv"

	c "github.com/DaniDeer/go-codex/codex"
)

// ── Memory (CLI byte-size convention) ──────────────────────────────────────────
//
// Wire: "512m", "1g", "128k", or a bare digit string ("536870912") —
// `docker run --memory`/`--memory-swap`/`--memory-reservation`'s own
// human-readable byte-size syntax. This is a GENUINE Docker CLI
// convention (not invented by Docker Compose, which reuses the exact
// same syntax for its own `mem_limit`/`mem_reservation` service keys) —
// so it lives here, in the general docker package, rather than in any
// Compose-specific model.
//
// [HostConfig.Memory]/[HostConfig.MemorySwap] themselves stay a bare
// int64 (Docker's create-options JSON wire form is already a plain
// number of bytes — see [HostConfigCodec]'s existing `c.Int64()` field
// codecs). [MemBytesCodec] is an ADDITIONAL codec for this alternate,
// STRING-suffixed CLI wire form of the SAME int64 Go representation —
// mirrors how the sibling iotedge package's CreateOptionsFieldCodec vs.
// [CreateOptionsCodec] are two different wire shapes of the same Go
// value.

// memSizeUnit maps a case-insensitive single-letter suffix to its
// byte multiplier. Binary (1024-based) — matches Docker Engine's OWN
// `--memory` flag convention (`docker run --memory 1g` allocates
// 1073741824 bytes, not 1000000000) — NOT the Compose specification's
// own (arguably ambiguous/inconsistent) prose about decimal vs. binary
// units. Since this value ultimately feeds [HostConfig.Memory], the
// Docker Engine convention is the one that matters.
var memSizeUnit = map[byte]int64{
	'b': 1,
	'k': 1024,
	'm': 1024 * 1024,
	'g': 1024 * 1024 * 1024,
}

// ParseMemBytes parses a Docker CLI-style human byte-size string (e.g.
// "512m", "1g", "128k", or a bare digit string meaning bytes) into a
// plain byte count. Returns a [MemSizeError] for anything else
// (empty string, non-digit prefix, unrecognized suffix, or a suffix
// with no preceding digits).
func ParseMemBytes(s string) (int64, error) {
	if s == "" {
		return 0, MemSizeError{Value: s, Reason: "empty string"}
	}

	last := s[len(s)-1]
	if last >= '0' && last <= '9' {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, MemSizeError{Value: s, Reason: "not a valid integer"}
		}
		if n < 0 {
			return 0, MemSizeError{Value: s, Reason: "negative size"}
		}
		return n, nil
	}

	unit, ok := memSizeUnit[toLowerASCII(last)]
	if !ok {
		return 0, MemSizeError{Value: s, Reason: fmt.Sprintf("unrecognized unit suffix %q", string(last))}
	}
	digits := s[:len(s)-1]
	if digits == "" {
		return 0, MemSizeError{Value: s, Reason: "missing digits before unit suffix"}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, MemSizeError{Value: s, Reason: "not a valid integer"}
	}
	if n < 0 {
		return 0, MemSizeError{Value: s, Reason: "negative size"}
	}
	return n * unit, nil
}

// FormatMemBytes formats n as the largest whole unit that divides it
// evenly (e.g. 1073741824 -> "1g", 1536 -> "1536b" since 1536 isn't a
// whole number of kibibytes) — the Encode-direction round trip for
// [MemBytesCodec]. Falls back to a bare byte count ("b" suffix) when no
// larger unit divides n evenly, which is always a safe, unambiguous
// representation.
func FormatMemBytes(n int64) string {
	switch {
	case n != 0 && n%(1024*1024*1024) == 0:
		return strconv.FormatInt(n/(1024*1024*1024), 10) + "g"
	case n != 0 && n%(1024*1024) == 0:
		return strconv.FormatInt(n/(1024*1024), 10) + "m"
	case n != 0 && n%1024 == 0:
		return strconv.FormatInt(n/1024, 10) + "k"
	default:
		return strconv.FormatInt(n, 10) + "b"
	}
}

func toLowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// MemSizeError is returned by [ParseMemBytes] (directly, or via
// [MemBytesCodec]'s decode direction) when a byte-size string doesn't
// match Docker's CLI convention.
type MemSizeError struct {
	Value  string
	Reason string
}

func (e MemSizeError) Error() string {
	return fmt.Sprintf("invalid memory size %q: %s", e.Value, e.Reason)
}

// MemBytesCodec decodes/encodes Docker's CLI-style human byte-size
// string convention (see [ParseMemBytes]/[FormatMemBytes]) as a plain
// int64 byte count — the same Go representation [HostConfig.Memory]/
// [HostConfig.MemorySwap] already use for the create-options JSON wire
// form (a bare number), just with this additional STRING wire shape for
// contexts that use Docker's CLI convention instead (e.g. Docker
// Compose's own `mem_limit`/`mem_reservation` service keys).
var MemBytesCodec = c.MapCodecValidated(
	c.String(), c.Int64(),
	func(s string) (int64, error) { return ParseMemBytes(s) },
	func(n int64) (string, error) { return FormatMemBytes(n), nil },
)
