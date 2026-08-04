package iotedge

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
)

// ModuleKeyPrefix is the fixed namespace for all module keys, e.g.
// "properties.desired.modules.cv-writer-kvrocks". Exported so a caller
// composing their own dotted-key codec (e.g. a patch targeting one module
// by name) can reuse the exact same prefix instead of duplicating it.
const ModuleKeyPrefix = "properties.desired.modules."

// moduleKeyConstraint validates the FULL wire key: must start with
// ModuleKeyPrefix and have a non-empty module-name segment after it.
var moduleKeyConstraint = c.Constraint[string]{
	Name: "module-key",
	Check: func(s string) bool {
		return strings.HasPrefix(s, ModuleKeyPrefix) && len(strings.TrimPrefix(s, ModuleKeyPrefix)) > 0
	},
	Message: func(s string) string {
		return fmt.Sprintf("key %q must start with %q followed by a module name", s, ModuleKeyPrefix)
	},
}
