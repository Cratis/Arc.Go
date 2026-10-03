// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/ContractTests/taskboard"
)

func TestConformanceGateRejectsVacuousCaseSelection(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTaskBoardHTTPConformance$/^httptest$/^no-such-case$", "-test.timeout=8s")
	output := &hostOutput{ready: make(chan string, 1)}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.Contains(output.String(), "vacuous conformance run: executed 0 of 9 cases") {
		t.Fatalf("empty selection was not rejected: %v\n%s", err, output.String())
	}
}

type brokenReadiness struct{ err error }

func (w brokenReadiness) Write([]byte) (int, error) { return 0, w.err }

func TestTaskBoardFailsWhenReadinessCannotBePublished(t *testing.T) {
	failure := errors.New("readiness output unavailable")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := taskboard.Run(ctx, brokenReadiness{failure}); !errors.Is(err, failure) {
		t.Fatalf("host error = %v", err)
	}
}
