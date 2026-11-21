//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"os"
	"testing"
)

// runKnownFlakesEnv opts in to tests that are skipped because of a known
// upstream kernel defect. Set it to "1" to verify a kernel fix before removing
// the corresponding skip.
const runKnownFlakesEnv = "ARC_CHRONICLE_RUN_KNOWN_FLAKES"

// chronicle6064 names the upstream kernel defect where a recovered position is
// delivered once more after partition recovery succeeds.
const chronicle6064 = "known upstream kernel defect https://github.com/Cratis/Chronicle/issues/6064"

// redeliveredAfterRecovery quarantines Chronicle#6064 only when the history holds
// exactly one extra prefix and suffix and the application effect still ran once.
// Any other shape fails, so an unrelated regression is not hidden.
func redeliveredAfterRecovery(t *testing.T, steps []string, state *redeliveryState) {
	t.Helper()
	counts := map[string]int{}
	for _, step := range steps {
		counts[step]++
	}
	if len(steps) != 5 || counts["prefix"] != 3 || counts["suffix"] != 2 {
		t.Fatal("unexpected committed command order", steps)
	}
	calls, applied := state.snapshot()
	if applied != 1 {
		t.Fatalf("application effect count = %d, want 1", applied)
	}
	knownKernelDefectObserved(t, chronicle6064, steps, calls, applied)
}

func runKnownFlakes() bool {
	return os.Getenv(runKnownFlakesEnv) == "1"
}

// knownKernelDefectObserved ends a test whose failure matched the signature of
// a known upstream kernel defect. It skips by default and fails when
// ARC_CHRONICLE_RUN_KNOWN_FLAKES=1, so the evidence stays visible either way.
func knownKernelDefectObserved(t *testing.T, reason string, evidence ...any) {
	t.Helper()
	t.Log(evidence...)
	if runKnownFlakes() {
		t.Fatal(reason)
	}
	t.Skip(reason + " (set " + runKnownFlakesEnv + "=1 to fail instead)")
}
