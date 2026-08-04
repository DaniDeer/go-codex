package docker

import v "github.com/DaniDeer/go-codex/validate"

// ulimitNameConstraint validates a Ulimit's Name against Docker's full
// documented `--ulimit` name list (the standard Linux RLIMIT_* resource
// names, in Docker's lowercase convention) — Docker itself rejects any
// other name at the daemon level, so this mirrors real, enforced behavior
// rather than being an arbitrarily narrow choice.
var ulimitNameConstraint = v.OneOf(
	"as", "core", "cpu", "data", "fsize", "locks", "memlock", "msgqueue",
	"nice", "nofile", "nproc", "rss", "rtprio", "rttime", "sigpending", "stack",
)
