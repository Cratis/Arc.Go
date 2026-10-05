---
title: Serve browsers on other origins
description: Protect Arc from cross-site requests and allow a trusted frontend origin with the standard library.
---

Your React frontend runs on `https://app.example.com` and Arc runs on another
origin. Browsers need CORS headers before they let the frontend read Arc's
responses, and Arc's command endpoints must not accept a POST forged by another
site that rides on the user's cookies. The host supplies CORS and CSRF;
Arc separately checks origins on observable WebSocket and hub SSE transports.
This recipe configures that allow-list and wraps the application with the
standard library's `http.CrossOriginProtection` and a small CORS handler.

## Wrap the application

This code is compiled and tested in the [recipes module](index.md).
Before building Arc, set its observable origins to the same exact origins you
will pass to `Protect`. Here `frontend` is `"https://app.example.com"`; pass
`options` to `arc.NewBuilder`:

```go
options := arc.Options{
    Observable: arc.ObservableOptions{AllowedOrigins: []string{frontend}},
}
```

Without this builder option, trusted cross-origin direct and hub WebSockets
and hub SSE still receive 403, even though CORS and CSRF allow the frontend.
After starting the application, wrap it:

```go
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
```

Serve the returned handler instead of `app`. `CrossOriginProtection` reads the
browser's `Sec-Fetch-Site` header, or compares `Origin` with `Host` for older
browsers. It treats GET, HEAD and OPTIONS as safe and checks every other
method, so QUERY is checked like POST.

## What reaches Arc

| Request | Result |
| --- | --- |
| Server-to-server, no `Origin` or `Sec-Fetch-Site` | Unchanged Arc behavior |
| Same-origin browser request | Unchanged Arc behavior |
| Cross-site POST or QUERY from the trusted origin | Arc runs; response carries `Access-Control-Allow-Origin` |
| Cross-site POST or QUERY from any other origin | 403 before Arc; the command does not run |
| Cross-site snapshot GET or direct SSE from any other origin | Arc runs; no CORS headers, so the browser hides the response |
| Direct or hub WebSocket, or hub SSE, from the trusted origin | 101 upgrade or 200 stream when `Observable.AllowedOrigins` matches |
| Direct or hub WebSocket, or hub SSE, from any other cross-origin site | 403 from Arc's transport origin check |
| Preflight from the trusted origin | 204 with the allowed methods and headers |
| Preflight from any other origin | No CORS grant |

:::caution[Exact origins only]
Trust exact `scheme://host[:port]` origins. Never reflect an arbitrary `Origin`
while also sending `Access-Control-Allow-Credentials: true`; that hands every
site the user's session. Add the headers your frontend sends to
`Access-Control-Allow-Headers`.
:::

The test also runs the full mount contract through the wrapper: commands,
`/validate`, GET, HEAD, QUERY, Arc's empty 404/405, request cancellation, direct
SSE/WebSocket and source cleanup on disconnect and application shutdown.
Browser-origin tests also check trusted and attacker direct/hub WebSocket
handshakes and hub SSE. Preflights allow `X-Ignore-Warnings`, and trusted
responses expose `X-Correlation-ID` so JavaScript can read the correlation ID.
The wrapper passes the original ResponseWriter through; it does not remove
flushing or hijacking capabilities. Forwarded headers are not trusted or parsed;
configure any proxy-derived identity or address handling separately at ingress.

## When this is the wrong fit

If an ingress or API gateway already applies CORS and CSRF policy, do not add a
second policy here; conflicting `Vary` and `Access-Control-*` headers are hard
to debug. Requires Go 1.25 or later for `http.CrossOriginProtection`, which
Arc.Go's Go 1.26 minimum satisfies.
