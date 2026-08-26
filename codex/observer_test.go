package codex_test

import (
	"time"

	"github.com/DaniDeer/go-codex/codex"
)

// fakeReloadObserver is a minimal, stats-free type satisfying
// codex.ReloadObserver — proves the structural-satisfaction design
// works standalone, with no dependency on the stats package at all.
type fakeReloadObserver struct{}

func (fakeReloadObserver) RecordReload(_ string, _ bool, _ time.Duration) {}

var _ codex.ReloadObserver = fakeReloadObserver{}
