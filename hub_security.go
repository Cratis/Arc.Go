// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/internal/streaming"
)

const hubCookiePrefix = "cratis-observable-"
const hubSSEPath = "/.cratis/queries/sse"

func transportOrigin(text string) (string, error) {
	if text == "null" {
		return text, nil
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", ErrInvalidOptions
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", ErrInvalidOptions
	}
	port := u.Port()
	if port == "80" && u.Scheme == "http" || port == "443" && u.Scheme == "https" {
		port = ""
	}
	if strings.Contains(host, "*") || strings.ContainsAny(host, " \t\r\n") {
		return "", ErrInvalidOptions
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host, nil
}
func (a *Application) hubOriginAllowed(r *http.Request) bool {
	values := headerValues(r.Header, "Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 || values[0] == "" {
		return false
	}
	origin, err := transportOrigin(values[0])
	if err != nil {
		return false
	}
	if slices.Contains(a.options.Observable.AllowedOrigins, origin) {
		return true
	}
	if origin == "null" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	same, err := transportOrigin(scheme + "://" + r.Host)
	return err == nil && origin == same
}
func opaqueHubID() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
func actualPeer(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (a *Application) hubOwner(r *http.Request, id string, opening bool) (streaming.ConnectionOwner, string, error) {
	principal, _ := identity.PrincipalFrom(r.Context())
	evidence := ""
	if !principal.IsAuthenticated() {
		if resolver := a.options.Observable.AnonymousOwner; resolver != nil {
			err := boundary.Call(r.Context(), func(ctx context.Context) error {
				var err error
				evidence, err = resolver(ctx, r.WithContext(ctx))
				return err
			})
			if err != nil || evidence == "" {
				return streaming.ConnectionOwner{}, "", errors.Join(errHubOwner, err)
			}
		} else if opening {
			var err error
			evidence, err = opaqueHubID()
			if err != nil {
				return streaming.ConnectionOwner{}, "", err
			}
		} else {
			for _, cookie := range r.Cookies() {
				if cookie.Name == hubCookiePrefix+id {
					if evidence != "" {
						return streaming.ConnectionOwner{}, "", streaming.ErrControl
					}
					evidence = cookie.Value
				}
			}
		}
	}
	owner, err := streaming.NewConnectionOwner(r.Context(), evidence)
	return owner, evidence, err
}
func (a *Application) setHubCookie(w http.ResponseWriter, r *http.Request, id, evidence string) {
	principal, _ := identity.PrincipalFrom(r.Context())
	if principal.IsAuthenticated() || a.options.Observable.AnonymousOwner != nil {
		return
	}
	lifetime := a.options.Observable.ConnectionLifetime
	http.SetCookie(w, &http.Cookie{Name: hubCookiePrefix + id, Value: evidence, Path: hubSSEPath, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(lifetime / time.Second), Expires: time.Now().Add(lifetime)})
}
func (a *Application) expireHubCookies(w http.ResponseWriter, r *http.Request) {
	// Only eligible hub responses clean cookies; cap deletion headers even when a
	// client supplies many forged names. Cookie values never enter logs.
	removed := 0
	for _, cookie := range r.Cookies() {
		if !strings.HasPrefix(cookie.Name, hubCookiePrefix) {
			continue
		}
		id := strings.TrimPrefix(cookie.Name, hubCookiePrefix)
		a.hubs.mu.Lock()
		connection := a.hubs.connections[id]
		live := connection != nil && connection.ctx.Err() == nil
		a.hubs.mu.Unlock()
		if live {
			continue
		}
		http.SetCookie(w, &http.Cookie{Name: cookie.Name, Path: hubSSEPath, MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		removed++
		if removed == 32 {
			return
		}
	}
}

var errHubOwner = errors.New("observable hub owner unavailable")
