package arc

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwitchingProtocolsCommitsTheObservedResponse(t *testing.T) {
	base := &responseWriter{ResponseWriter: httptest.NewRecorder()}
	base.WriteHeader(http.StatusContinue)
	if base.status != 0 {
		t.Fatal("informational response committed", base.status)
	}
	base.WriteHeader(http.StatusSwitchingProtocols)
	if base.status != http.StatusSwitchingProtocols {
		t.Fatal("protocol switch not committed", base.status)
	}
}

func TestOwnedServerUsesConfiguredSlogErrorLogger(t *testing.T) {
	var logs bytes.Buffer
	a := &Application{options: Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))}}
	server := a.newHTTPServer()
	if server.ErrorLog == nil {
		t.Fatal("owned server uses the global logger")
	}
	server.ErrorLog.Print("server diagnostic")
	if !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), "server diagnostic") {
		t.Fatal(logs.String())
	}
	a.options.Logger = nil
	if a.newHTTPServer().ErrorLog == nil {
		t.Fatal("nil options logger falls back to global logs")
	}
}
