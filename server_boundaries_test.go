package arc_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
)

func TestRealListenerDisconnectCancelsAndJoinsRequest(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle("GET /wait", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(canceled) })); err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	serveCtx, stopServer := context.WithCancel(t.Context())
	serveDone := make(chan error, 1)
	go func() { serveDone <- a.Serve(serveCtx, listener) }()
	joined := false
	t.Cleanup(func() {
		stopServer()
		if !joined {
			if err := <-serveDone; err != nil {
				t.Error(err)
			}
		}
	})
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request, err := http.NewRequestWithContext(requestCtx, "GET", "http://"+listener.Addr().String()+"/wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			err = response.Body.Close()
		}
		requestDone <- err
	}()
	<-entered
	cancelRequest()
	<-canceled
	<-requestDone
	stopServer()
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
	joined = true
}
func TestOwnedServerBoundsSlowHeaders(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{HTTP: arc.HTTPOptions{ReadHeaderTimeout: 100 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\n"); err != nil {
		t.Fatal(err)
	}
	var body [1024]byte
	_, err = connection.Read(body[:])
	if err == nil {
		t.Fatal("incomplete headers accepted")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("server did not bound slow headers", err)
	}
}
