// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/metadata"
)

type rawHandler struct {
	pattern string
	handler http.Handler
}

var frameworkPaths = []string{"/.cratis/me", "/.cratis/commands", "/.cratis/queries", "/.cratis/identity-details/schema", "/.cratis/users", "/.cratis/tenants"}

// Handle registers a borrowed raw handler with normal ServeMux pattern syntax.
// Exact Arc/reserved-path ownership conflicts fail Build. Overlapping subtree
// and catch-all handlers are fallbacks: Arc routes and /.cratis/* always win.
func (b *Builder) Handle(pattern string, handler http.Handler) error {
	if b.attempted {
		return ErrFrozen
	}
	if nilValue(handler) || pattern == "" {
		return ErrInvalidOptions
	}
	b.rawHandlers = append(b.rawHandlers, rawHandler{pattern, handler})
	return nil
}

func (a *Application) compileRoutes(raw []rawHandler) error {
	access, err := discoveryAccess(a.options)
	if err != nil {
		return err
	}
	a.discovery = access
	a.authentication, err = authentication.New(a.options.Authentication...)
	if err != nil {
		return err
	}
	a.routeTable = make(map[string]map[string]metadata.Endpoint)
	endpoints := slices.Clone(a.endpoints)
	for _, e := range a.endpoints {
		if e.Method == "GET" {
			h := e
			h.Method = "HEAD"
			endpoints = append(endpoints, h)
		}
	}
	for _, path := range frameworkPaths {
		if path != "/.cratis/me" && access == discoveryUnavailable {
			continue
		}
		if (path == "/.cratis/commands" || path == "/.cratis/queries") && a.options.Introspection.Enabled != nil && !*a.options.Introspection.Enabled {
			continue
		}
		for _, method := range []string{"GET", "HEAD"} {
			endpoints = append(endpoints, metadata.Endpoint{Identity: path, Method: method, Path: path})
		}
	}
	for _, path := range []string{hubWSPath, hubSSEPath, hubSSEPath + "/subscribe", hubSSEPath + "/unsubscribe"} {
		methods := []string{"POST"}
		if path == hubSSEPath || path == hubWSPath {
			methods = []string{"GET", "HEAD"}
		}
		for _, method := range methods {
			endpoints = append(endpoints, metadata.Endpoint{Identity: path, Method: method, Path: path})
		}
	}
	for _, e := range endpoints {
		if !strings.HasPrefix(e.Identity, "/.cratis/") && reservedPath(e.Path) {
			return ErrRouteConflict
		}
		for path := range a.routeTable {
			if path != e.Path && strings.EqualFold(path, e.Path) {
				return ErrRouteConflict
			}
		}
		methods := a.routeTable[e.Path]
		if methods == nil {
			methods = make(map[string]metadata.Endpoint)
			a.routeTable[e.Path] = methods
		}
		if _, exists := methods[e.Method]; exists {
			return ErrRouteConflict
		}
		methods[e.Method] = e
	}
	custom := http.NewServeMux()
	for _, h := range raw {
		if err := muxHandle(custom, h.pattern, h.handler); err != nil {
			return err
		}
		// Probe exact patterns in isolation, including host constraints. Subtree
		// and wildcard patterns are allowed fallbacks, never Arc route owners.
		probe := http.NewServeMux()
		if err := muxHandle(probe, h.pattern, h.handler); err != nil {
			return err
		}
		pattern := h.pattern
		if _, rest, ok := strings.Cut(pattern, " "); ok {
			pattern = rest
		}
		host := "example.invalid"
		if i := strings.IndexByte(pattern, '/'); i > 0 {
			host = pattern[:i]
			pattern = pattern[i:]
		}
		exact := strings.TrimSuffix(pattern, "{$}")
		if strings.Contains(exact, "{") || strings.HasSuffix(pattern, "/") {
			continue
		}
		if reservedPath(exact) {
			return fmt.Errorf("%w: %s owns reserved path %s", ErrRouteConflict, h.pattern, exact)
		}
		paths := slices.Clone(frameworkPaths)
		for path := range a.routeTable {
			paths = append(paths, path)
		}
		method := "ARC-PROBE"
		if first, _, ok := strings.Cut(h.pattern, " "); ok {
			method = first
		}
		for _, path := range paths {
			if path != exact {
				continue
			}
			for _, m := range []string{method, "GET", "HEAD", "POST", "QUERY", "OPTIONS"} {
				r := &http.Request{Method: m, Host: host, URL: &url.URL{Path: path}}
				if _, p := probe.Handler(r); p != "" {
					return fmt.Errorf("%w: %s overlaps %s", ErrRouteConflict, h.pattern, path)
				}
			}
		}
	}
	mux := http.NewServeMux()
	for path, methods := range a.routeTable {
		names := make([]string, 0, len(methods))
		for method := range methods {
			names = append(names, method)
		}
		slices.Sort(names)
		allow := strings.Join(names, ", ")
		if err := muxHandle(mux, literalPattern(path), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Allow", allow)
			w.WriteHeader(http.StatusMethodNotAllowed)
		})); err != nil {
			return err
		}
		for _, e := range methods {
			if err := muxHandle(mux, e.Method+" "+literalPattern(path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.dispatch(w, r, e) })); err != nil {
				return err
			}
		}
	}
	a.rawMux = custom
	a.endpoints = endpoints
	a.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, known := a.routeTable[r.URL.Path]; known {
			mux.ServeHTTP(w, r)
			return
		}
		if reservedPath(r.URL.Path) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler, pattern := custom.Handler(r)
		if pattern != "" {
			handler.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return nil
}
func reservedPath(path string) bool {
	return strings.EqualFold(path, "/.cratis") || strings.HasPrefix(strings.ToLower(path), "/.cratis/")
}

func muxHandle(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%w: %v", ErrRouteConflict, v)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}
func literalPattern(path string) string {
	if strings.HasSuffix(path, "/") {
		return path + "{$}"
	}
	return path
}
func canonicalPath(r *http.Request) bool {
	if r.URL == nil || !strings.HasPrefix(r.URL.Path, "/") || strings.ContainsAny(r.URL.Path, "\\") || strings.Contains(r.URL.Path, "//") {
		return false
	}
	for _, segment := range strings.Split(r.URL.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	canonical := (&url.URL{Path: r.URL.Path}).EscapedPath()
	return r.URL.EscapedPath() == canonical
}
func (a *Application) dispatch(w http.ResponseWriter, r *http.Request, e metadata.Endpoint) {
	switch e.Path {
	case hubWSPath:
		a.hubWebSocketEndpoint(w, r)
		return
	case hubSSEPath:
		a.hubSSEEndpoint(w, r)
		return
	case hubSSEPath + "/subscribe":
		a.hubSSEControl(w, r, true)
		return
	case hubSSEPath + "/unsubscribe":
		a.hubSSEControl(w, r, false)
		return
	}
	if e.Method == "POST" {
		a.commandEndpoint(w, r, e)
		return
	}
	if !strings.HasPrefix(e.Identity, "/.cratis/") {
		a.queryEndpoint(w, r, e)
		return
	}
	if e.Path == "/.cratis/me" {
		a.identityEndpoint(w, r)
		return
	}
	a.discoveryEndpoint(w, r)
}

type discoveryMode uint8

const (
	discoveryUnavailable discoveryMode = iota
	discoveryAnonymous
	discoveryAuthenticated
)

func discoveryAccess(o Options) (discoveryMode, error) {
	for _, role := range o.Introspection.Roles {
		if strings.TrimSpace(role) == "" {
			return 0, ErrInvalidOptions
		}
	}
	required := o.Environment != "Development"
	explicit := o.Introspection.RequireAuthentication
	if explicit != nil {
		required = *explicit
	}
	if len(o.Introspection.Roles) > 0 {
		if explicit != nil && !*explicit {
			return 0, ErrInvalidOptions
		}
		required = true
	}
	if !required {
		return discoveryAnonymous, nil
	}
	if len(o.Authentication) == 0 {
		if explicit != nil && *explicit || len(o.Introspection.Roles) > 0 {
			return 0, ErrAuthenticationSetup
		}
		return discoveryUnavailable, nil
	}
	return discoveryAuthenticated, nil
}
