// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
)

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
		return 0, fmt.Errorf("host exceeded 64 KiB log limit")
	}
	n, err := w.buffer.Write(p)
	if !w.sent {
		if i := bytes.IndexByte(w.buffer.Bytes(), '\n'); i >= 0 {
			w.sent = true
			w.ready <- string(w.buffer.Bytes()[:i])
		}
	}
	return n, err
}
func (w *hostOutput) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buffer.String() }

type processHost struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{}
	waitErr error
	output  *hostOutput
	cancel  context.CancelFunc
}

func launch(ctx context.Context, command string, args, env []string) (*processHost, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	h := &processHost{done: make(chan struct{}), output: &hostOutput{ready: make(chan string, 1)}, cancel: cancel}
	h.cmd = exec.CommandContext(ctx, command, args...)
	h.cmd.Env = append(os.Environ(), env...)
	h.cmd.Stdout, h.cmd.Stderr = h.output, h.output
	h.cmd.WaitDelay = 3 * time.Second
	stdin, err := h.cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	h.stdin = stdin
	if err := h.cmd.Start(); err != nil {
		cancel()
		return nil, errors.Join(err, stdin.Close())
	}
	go func() { h.waitErr = h.cmd.Wait(); close(h.done) }()
	return h, nil
}
func (h *processHost) origin(ctx context.Context) (string, error) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case line := <-h.output.ready:
		var ready struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"baseUrl"`
		}
		d := json.NewDecoder(bytes.NewBufferString(line))
		d.DisallowUnknownFields()
		if err := d.Decode(&ready); err != nil {
			return "", fmt.Errorf("readiness: %w: %s", err, line)
		}
		if _, err := d.Token(); err != io.EOF {
			return "", fmt.Errorf("trailing readiness JSON")
		}
		if ready.Kind != "httpconformance-ready" {
			return "", fmt.Errorf("unexpected readiness %q", ready.Kind)
		}
		if err := validateOrigin(ready.BaseURL); err != nil {
			return "", err
		}
		return ready.BaseURL, nil
	case <-h.done:
		return "", fmt.Errorf("host exited before readiness: %w\n%s", h.waitErr, h.output.String())
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("readiness exceeded 10 seconds\n%s", h.output.String())
	}
}
func (h *processHost) stop(grace time.Duration) error {
	defer h.cancel()
	err := h.stdin.Close()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-h.done:
		return errors.Join(err, h.waitErr)
	case <-timer.C:
		err = errors.Join(err, fmt.Errorf("forced cleanup: host did not stop gracefully"), h.cmd.Process.Kill())
		join := time.NewTimer(3 * time.Second)
		defer join.Stop()
		select {
		case <-h.done:
			return errors.Join(err, h.waitErr)
		case <-join.C:
			return errors.Join(err, fmt.Errorf("host could not be joined"))
		}
	}
}
func start(t *testing.T, command string, args, env []string) string {
	t.Helper()
	h, err := launch(context.WithoutCancel(t.Context()), command, args, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.stop(5 * time.Second); err != nil {
			t.Errorf("host cleanup: %v\n%s", err, h.output.String())
		}
	})
	origin, err := h.origin(t.Context())
	if err != nil {
		t.Fatalf("host startup: %v\n%s", err, h.output.String())
	}
	return origin
}
func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("unsafe host origin %q", origin)
	}
	return nil
}

type readyListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() {
		l.err = json.NewEncoder(os.Stdout).Encode(map[string]string{"kind": "httpconformance-ready", "baseUrl": "http://" + l.Addr().String()})
		if l.err != nil {
			l.err = errors.Join(l.err, l.Close())
		}
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.Listener.Accept()
}
func TestHostChild(t *testing.T) {
	mode := os.Getenv("ARC_HTTP_CONFORMANCE_CHILD")
	if mode == "" {
		return
	}
	if mode == "early" {
		return
	}
	if mode == "refuse" {
		fmt.Println(`{"kind":"httpconformance-ready","baseUrl":"http://127.0.0.1:1"}`)
		<-t.Context().Done()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	app, err := application()
	if err == nil {
		var listener net.Listener
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
		if err == nil {
			err = app.Serve(ctx, &readyListener{Listener: listener})
		}
	}
	_ = os.Stdin.Close()
	<-done
	if err != nil {
		t.Fatal(err)
	}
}
func child(t *testing.T, mode string) (string, []string, []string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return executable, []string{"-test.run=^TestHostChild$", "-test.timeout=80s"}, []string{"ARC_HTTP_CONFORMANCE_CHILD=" + mode}
}

func TestGoHostLifecycle(t *testing.T) {
	command, args, env := child(t, "go")
	start(t, command, args, env)
}
func TestMissingHostFails(t *testing.T) {
	if _, err := launch(t.Context(), "/nonexistent-httpconformance-host", nil, nil); err == nil {
		t.Fatal("missing host admitted")
	}
}
func TestEarlyExitFails(t *testing.T) {
	command, args, env := child(t, "early")
	h, err := launch(t.Context(), command, args, env)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.origin(t.Context())
	cleanup := h.stop(5 * time.Second)
	if err == nil {
		t.Fatal("early exit admitted")
	}
	if cleanup != nil {
		t.Fatal(cleanup)
	}
}
func TestForcedCleanupFailsAndJoins(t *testing.T) {
	command, args, env := child(t, "refuse")
	h, err := launch(t.Context(), command, args, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.origin(t.Context()); err != nil {
		_ = h.stop(time.Second)
		t.Fatal(err)
	}
	if err := h.stop(50 * time.Millisecond); err == nil {
		t.Fatal("forced cleanup admitted")
	}
	select {
	case <-h.done:
	default:
		t.Fatal("process not joined")
	}
}
func TestCanceledReadinessFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h := &processHost{done: make(chan struct{}), output: &hostOutput{ready: make(chan string)}}
	if _, err := h.origin(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled readiness = %v", err)
	}
}

func TestOutputBoundsAndSignalsOnce(t *testing.T) {
	o := &hostOutput{ready: make(chan string, 1)}
	if _, err := o.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-o.ready:
		t.Fatal("partial readiness")
	default:
	}
	if _, err := o.Write([]byte(" line\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	if line := <-o.ready; line != "first line" {
		t.Fatal(line)
	}
	if _, err := o.Write(make([]byte, 64*1024)); err == nil {
		t.Fatal("unbounded logs")
	}
	select {
	case <-o.ready:
		t.Fatal("multiple readiness signals")
	default:
	}
}
func TestRejectUnsafeOrigins(t *testing.T) {
	for _, origin := range []string{"http://localhost:1", "https://127.0.0.1:1", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://user@127.0.0.1:1", "http://127.0.0.1:1/", "http://127.0.0.1:1?", "http://127.0.0.1:1#x"} {
		if validateOrigin(origin) == nil {
			t.Errorf("accepted %q", origin)
		}
	}
}
