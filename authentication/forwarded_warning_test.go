package authentication_test

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/cratis/arc.go/authentication"
)

// A subprocess gives the process-wide warning a fresh lifetime without resetting
// shared production state or depending on the order/count of other tests.
func TestUntrustedForwardedIdentityWarnsOncePerProcess(t *testing.T) {
	const child = "ARC_GO_TEST_FORWARDED_WARNING"
	if os.Getenv(child) == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), binary, "-test.run=^TestUntrustedForwardedIdentityWarnsOncePerProcess$", "-test.count=1", "-test.timeout=30s")
		command.Env = append(os.Environ(), child+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		return
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	var handlers []authentication.Handler
	for range 2 {
		handler, err := authentication.MicrosoftIdentityPlatform(authentication.MicrosoftIdentityOptions{Logger: logger})
		if err != nil {
			t.Fatal(err)
		}
		handlers = append(handlers, handler)
	}
	for _, h := range handlers {
		request := httptest.NewRequest("GET", "/", nil)
		request.Header.Set("Authorization", "SECRET")
		if _, err := h.Authenticate(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	if logs.Len() != 0 {
		t.Fatal("warned without forwarded headers", logs.String())
	}
	trusted, err := authentication.MicrosoftIdentityPlatform(authentication.MicrosoftIdentityOptions{Logger: logger, TrustForwardedIdentityHeaders: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("X-MS-CLIENT-PRINCIPAL", "SECRET")
	if _, err := trusted.Authenticate(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatal("trusted ingress warned", logs.String())
	}
	var joined sync.WaitGroup
	for _, name := range []string{"x-ms-client-principal", "X-MS-Client-Principal-ID", "x-ms-client-principal-name"} {
		for _, h := range handlers {
			joined.Add(1)
			go func() {
				defer joined.Done()
				r := httptest.NewRequest("GET", "/", nil)
				r.Header[name] = []string{"SECRET header"}
				result, err := h.Authenticate(t.Context(), r)
				if _, authenticated := result.Principal(); err != nil || authenticated || result.Failure() != nil {
					t.Error("untrusted header became authority", err)
				}
			}()
		}
	}
	joined.Wait()
	if strings.Count(logs.String(), `"level":"WARN"`) != 1 || strings.Contains(logs.String(), "SECRET") || !strings.Contains(logs.String(), "TrustForwardedIdentityHeaders") {
		t.Fatal(logs.String())
	}
}
