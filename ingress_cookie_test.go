// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"net/http/httptest"
	"testing"

	arc "github.com/cratis/arc.go"
)

func TestLegacyCookieSecureExceptPlainHTTPLocalDevelopment(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin, forwardedProto, forwarded string
		secure                                  bool
	}{
		{"TLS", "https://localhost", "", "", true},
		{"remote HTTP", "http://example.com", "", "", true},
		{"HTTPS ingress", "http://example.com", "https", "", true},
		{"forwarded HTTP cannot relax", "https://example.com", "http", "proto=http", true},
		{"remote forwarded HTTP cannot relax", "http://example.com", "http", "proto=http", true},
		{"localhost HTTPS ingress", "http://localhost:8080", "https", "", true},
		{"loopback HTTPS ingress", "http://127.0.0.1:8080", "https", "", true},
		{"forwarded localhost HTTPS", "http://localhost:8080", "", "for=192.0.2.1;proto=https", true},
		{"forwarded loopback HTTPS", "http://[::1]:8080", "", `for="[::1]"; proto="HTTPS"`, true},
		{"multiple proxy protocols", "http://localhost:8080", "http, https", "", true},
		{"multiple forwarded elements", "http://localhost:8080", "", "proto=http, proto=https", true},
		{"localhost HTTP", "http://localhost:8080", "", "", false},
		{"localhost forwarded HTTP", "http://localhost:8080", "http", "proto=http", false},
		{"loopback IPv4 HTTP", "http://127.0.0.1:8080", "", "", false},
		{"loopback IPv6 HTTP", "http://[::1]:8080", "", "", false},
		{"localhost lookalike", "http://localhost.example.com", "", "", true},
		{"private address is not loopback", "http://192.168.1.1", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.origin+"/.cratis/me", nil)
			r.Header.Set("Cookie", ".cratis-identity=legacy")
			r.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			r.Header.Set("Forwarded", tc.forwarded)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			response := w.Result()
			cookies := response.Cookies()
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if len(cookies) != 1 || cookies[0].Secure != tc.secure {
				t.Fatalf("cookies = %v; want Secure=%v", cookies, tc.secure)
			}
		})
	}
}
