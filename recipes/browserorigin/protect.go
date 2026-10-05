// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package browserorigin wraps an Arc application for browser frontends on
// other origins with standard-library CSRF protection and explicit CORS.
package browserorigin

import (
	"net/http"
)

// recipe:start cors-csrf

// Protect wraps a started Arc application. Cross-origin browser requests are
// accepted only from the exact trusted origins, such as
// "https://app.example.com"; every other cross-site unsafe request (POST,
// QUERY) is rejected with 403 before Arc runs. At builder construction, also
// set Observable.AllowedOrigins to these origins for WebSocket and hub SSE.
func Protect(app http.Handler, trustedOrigins ...string) (http.Handler, error) {
	csrf := http.NewCrossOriginProtection()
	trusted := make(map[string]bool, len(trustedOrigins))
	for _, origin := range trustedOrigins {
		if err := csrf.AddTrustedOrigin(origin); err != nil {
			return nil, err
		}
		trusted[origin] = true
	}
	return cors(trusted, csrf.Handler(app)), nil
}

// cors answers preflights itself (Arc returns 405 for OPTIONS) and lets
// trusted origins read responses, including credentialed ones.
func cors(trusted map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if !trusted[origin] {
			next.ServeHTTP(w, r)
			return
		}
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Allow-Credentials", "true")
		if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
			header.Set("Access-Control-Expose-Headers", "X-Correlation-ID")
			next.ServeHTTP(w, r)
			return
		}
		header.Add("Vary", "Access-Control-Request-Method")
		header.Add("Vary", "Access-Control-Request-Headers")
		// QUERY is not CORS-safelisted, so browsers always preflight it.
		header.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, QUERY")
		header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Allowed-Severity, X-Ignore-Warnings, X-Correlation-ID, x-cratis-tenant-id")
		header.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
}

// recipe:end
