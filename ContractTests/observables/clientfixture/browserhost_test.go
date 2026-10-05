// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cratis/arc.go/ContractTests/observables/clientfixture"
)

func TestBrowserHostRequiresListenerAndReport(t *testing.T) {
	if err := clientfixture.ServeBrowser(t.Context(), nil, fstest.MapFS{}, func(clientfixture.BrowserReport) error { return nil }); err == nil {
		t.Fatal("missing listener must fail")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := clientfixture.ServeBrowser(t.Context(), listener, fstest.MapFS{}, nil); err == nil {
		t.Fatal("missing report must fail")
	}
	// The host owns the listener even when it refuses to start.
	if _, err := listener.Accept(); err == nil {
		t.Fatal("refused host left its listener open")
	}
}

func TestBrowserHostRefusesMissingAssetsAndClosesListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := clientfixture.ServeBrowser(t.Context(), listener, nil, func(clientfixture.BrowserReport) error { return nil }); err == nil {
		t.Fatal("missing assets must fail")
	}
	if _, err := listener.Accept(); err == nil {
		t.Fatal("refused host left its listener open")
	}
}

// Native regression for the restart control the browser lane depends on: a real
// server close joins every source, then the same address serves a new generation.
func TestBrowserHostRestartsOnSameAddressAndJoinsEveryGeneration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	reports := make(chan clientfixture.BrowserReport, 4)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- clientfixture.ServeBrowser(ctx, listener, fstest.MapFS{"index.html": {Data: []byte("<main>fixture</main>")}}, func(r clientfixture.BrowserReport) error {
			reports <- r
			return nil
		})
	}()
	stop := sync.OnceValue(func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(7 * time.Second):
			return errors.New("browser host did not join")
		}
	})
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})

	owner := browserFixtureClient(t, true)
	waitBrowserHostReady(t, owner, origin)
	hub := openBrowserFixtureHub(t, owner, origin)
	names := readyNames(t, owner, origin)
	control := map[string]any{"connectionId": hub.id, "queryId": "restart", "revision": 1, "request": map[string]any{"queryName": names["All"], "arguments": map[string]string{"group": "alpha"}}}
	postBrowserFixtureControl(t, owner, origin, "/subscribe", control, origin, http.StatusOK)
	if frame := hub.read(t); frame.Type != "QueryResult" {
		t.Fatalf("first generation did not deliver: %q", frame.Type)
	}
	response := browserFixtureRequest(t, owner, http.MethodPost, origin+"/fixture/shutdown", "", "")
	closeBrowserBody(t, response)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("restart control status=%d", response.StatusCode)
	}
	if hub.scanner.Scan() || hub.scanner.Err() != nil {
		t.Fatalf("server close did not end the live hub cleanly: %v", hub.scanner.Err())
	}
	restarted := nextBrowserReport(t, reports)
	if restarted.Restarted != 1 || restarted.Joined || restarted.Generation != 1 || restarted.Signals.Open != 1 || restarted.Signals.Close != 1 || restarted.Signals.Dispose != 1 {
		t.Fatalf("first generation report: %+v", restarted)
	}

	// The same address serves a fresh generation: new ownership, fresh baseline.
	waitBrowserHostReady(t, owner, origin)
	next := openBrowserFixtureHub(t, owner, origin)
	if next.id == hub.id {
		t.Fatal("restarted generation reused the retired connection ID")
	}
	control["connectionId"] = next.id
	postBrowserFixtureControl(t, owner, origin, "/subscribe", control, origin, http.StatusOK)
	if frame := next.read(t); frame.Type != "QueryResult" {
		t.Fatalf("second generation did not deliver: %q", frame.Type)
	}
	if err := stop(); err != nil {
		t.Fatalf("browser host: %v", err)
	}
	joined := nextBrowserReport(t, reports)
	if !joined.Joined || joined.Generation != 2 || joined.Signals.Open != 1 || joined.Signals.Close != 1 {
		t.Fatalf("final report: %+v", joined)
	}
	if next.scanner.Scan() || next.scanner.Err() != nil {
		t.Fatalf("joined shutdown did not end the live hub cleanly: %v", next.scanner.Err())
	}
}

func TestBrowserHostReturnsReportFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	failure := errors.New("report failed")
	done := make(chan error, 1)
	go func() {
		done <- clientfixture.ServeBrowser(t.Context(), listener, fstest.MapFS{}, func(clientfixture.BrowserReport) error { return failure })
	}()
	client := browserFixtureClient(t, false)
	waitBrowserHostReady(t, client, origin)
	response := browserFixtureRequest(t, client, http.MethodPost, origin+"/fixture/shutdown", "", "")
	closeBrowserBody(t, response)
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("report failure not returned: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("browser host did not stop after its report failed")
	}
	// A failed restart report must not leave the rebound listener open.
	if _, err := net.Dial("tcp", listener.Addr().String()); err == nil {
		t.Fatal("failed restart report left the rebound listener open")
	}
}

func waitBrowserHostReady(t *testing.T, client *http.Client, origin string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// The restarted listener is bound before Serve admits requests, so one
	// bounded request waits for admission; it is not a retry loop.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/fixture/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("browser host not ready: %v", err)
	}
	closeBrowserBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("browser host readiness status=%d", response.StatusCode)
	}
}

func readyNames(t *testing.T, client *http.Client, origin string) map[string]string {
	t.Helper()
	response := browserFixtureRequest(t, client, http.MethodGet, origin+"/fixture/ready", "", "")
	defer closeBrowserBody(t, response)
	var names map[string]string
	if err := json.NewDecoder(response.Body).Decode(&names); err != nil || names["All"] == "" {
		t.Fatalf("ready names: %v %v", names, err)
	}
	return names
}

func nextBrowserReport(t *testing.T, reports <-chan clientfixture.BrowserReport) clientfixture.BrowserReport {
	t.Helper()
	select {
	case report := <-reports:
		return report
	case <-time.After(7 * time.Second):
		t.Fatal("browser host report missing")
		return clientfixture.BrowserReport{}
	}
}
