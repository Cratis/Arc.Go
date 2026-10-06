// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/cratis/arc.go/ContractTests/internal/taskboard"
)

func TestTaskBoardHTTPConformance(t *testing.T) {
	executed := 0
	t.Run("httptest", func(t *testing.T) {
		executed++
		builder, err := taskboard.NewBuilder()
		if err != nil {
			t.Fatal(err)
		}
		app, err := builder.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := app.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		})
		server := httptest.NewServer(app)
		t.Cleanup(server.Close)
		exerciseTaskBoard(t, server.URL)
	})
	t.Run("process", func(t *testing.T) {
		executed++
		exerciseTaskBoard(t, startTaskBoard(t))
	})
	if executed == 0 {
		t.Fatal("vacuous conformance run: no host selected")
	}
}

// Reusing the compiled test executable avoids nested go builds and ensures the
// child is race-instrumented in the race lane. It calls the exact host function
// used by ContractTests/internal/taskboard/host, not a handwritten test HTTP handler.
func TestTaskBoardHostChild(t *testing.T) {
	if os.Getenv("ARC_GO_TASKBOARD_CHILD") != "1" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, os.Stdin) // EOF or read failure requests shutdown.
		cancel()
	}()
	err := taskboard.Run(ctx, os.Stdout)
	_ = os.Stdin.Close() // Unblock and join the reader if startup/serving failed.
	<-done
	if err != nil {
		t.Fatal(err)
	}
}

func startTaskBoard(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 60*time.Second)
	output := &hostOutput{ready: make(chan string, 1)}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTaskBoardHostChild$", "-test.timeout=55s")
	cmd.Env = append(os.Environ(), "ARC_GO_TASKBOARD_CHILD=1")
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		defer cancel()
		_ = stdin.Close() // Graceful, portable shutdown (signals differ on Windows).
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("conformance host did not stop gracefully; killing it")
			if err := cmd.Process.Kill(); err != nil {
				t.Errorf("kill host: %v", err)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("conformance host could not be joined")
				return
			}
		}
		if waitErr != nil {
			t.Errorf("conformance host exited: %v\n%s", waitErr, output.String())
		}
	})
	select {
	case line := <-output.ready:
		var ready struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"baseUrl"`
		}
		decoder := json.NewDecoder(bytes.NewBufferString(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&ready); err != nil {
			t.Fatalf("invalid readiness: %v: %s", err, line)
		}
		if ready.Kind != "arc-go-conformance-ready" {
			t.Fatalf("unexpected readiness kind %q", ready.Kind)
		}
		if err := validateOrigin(ready.BaseURL); err != nil {
			t.Fatal(err)
		}
		return ready.BaseURL
	case <-done:
		t.Fatalf("host exited before readiness: %v\n%s", waitErr, output.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("host readiness timed out\n%s", output.String())
	}
	return ""
}

// hostOutput bounds combined child logs and delivers readiness without polling or
// a separate scanner goroutine. exec owns and joins its output-copy workers.
type hostOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	ready  chan string
	sent   bool
}

func (w *hostOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer.Len()+len(p) > 64*1024 {
		return 0, fmt.Errorf("conformance host exceeded log limit")
	}
	n, err := w.buffer.Write(p)
	if !w.sent {
		if index := bytes.IndexByte(w.buffer.Bytes(), '\n'); index >= 0 {
			w.sent = true
			w.ready <- string(w.buffer.Bytes()[:index])
		}
	}
	return n, err
}
func (w *hostOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func TestHostOutputBoundsLogsAndSignalsOnce(t *testing.T) {
	output := &hostOutput{ready: make(chan string, 1)}
	if _, err := output.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-output.ready:
		t.Fatal("partial readiness signaled")
	default:
	}
	if _, err := output.Write([]byte(" line\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	if got := <-output.ready; got != "first line" {
		t.Fatalf("line = %q", got)
	}
	if _, err := output.Write(make([]byte, 64*1024)); err == nil {
		t.Fatal("unbounded logs accepted")
	}
	select {
	case <-output.ready:
		t.Fatal("readiness signaled twice")
	default:
	}
}

func TestConformanceOriginRejectsNonLoopbackAndCredentials(t *testing.T) {
	for _, origin := range []string{"https://127.0.0.1:80", "http://localhost:80", "http://127.0.0.1", "http://user@127.0.0.1:80", "http://127.0.0.1:80/path", "http://127.0.0.1:80?query=1", "http://127.0.0.1:80#fragment"} {
		if err := validateOrigin(origin); err == nil {
			t.Errorf("accepted %q", origin)
		}
	}
}
