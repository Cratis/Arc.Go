// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

//go:embed testdata/staticfiles/site/index.html testdata/staticfiles/site/assets/app.js testdata/staticfiles/site/assets/site.css
var staticSiteFiles embed.FS

type siteMessage struct {
	Message string `json:"message"`
}

// ExampleBuilder_Handle_staticFiles composes public, nonpersonalized site content
// with a closure query. The helper below belongs to this example, not Arc's API.
func ExampleBuilder_Handle_staticFiles() {
	if err := runStaticSiteExample(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// /assets/app.js 200
	// /app/tasks 200
	// /api/site-message 200
}

func runStaticSiteExample() error {
	site, err := fs.Sub(staticSiteFiles, "testdata/staticfiles/site")
	if err != nil {
		return err
	}
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		return err
	}
	message := siteMessage{Message: "Hello from Arc"}
	if err := queries.Register[siteMessage](builder, "Current", queries.Function(
		func(context.Context, queries.NoArguments) (siteMessage, error) {
			return message, nil
		}), queries.WithPath[queries.NoArguments]("/api/site-message"),
		queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})); err != nil {
		return err
	}
	if err := builder.Handle("/", newStaticSite(site)); err != nil {
		return err
	}
	app, err := builder.Build()
	if err != nil {
		return err
	}
	if err := app.Start(context.Background()); err != nil {
		return err
	}
	server := httptest.NewServer(app)
	requestErr := func() error {
		client := server.Client()
		client.Timeout = 5 * time.Second
		for _, target := range []string{"/assets/app.js", "/app/tasks", "/api/site-message"} {
			response, err := client.Get(server.URL + target)
			if err != nil {
				return err
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return err
			}
			fmt.Println(target, response.StatusCode)
		}
		return nil
	}()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErr := server.Config.Shutdown(cleanupCtx)
	server.Close() // Join the external host before shutting down Arc.
	return errors.Join(requestErr, serverErr, app.Shutdown(cleanupCtx))
}

// newStaticSite borrows an immutable embedded or trusted read-only ordinary FS.
// Open/Stat/Seek/Read/Close must finish promptly; fs.FS has no context contract.
// It is not a sandbox for writable directories, symlinks, or arbitrary FS code.
func newStaticSite(site fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = siteResponseWriter{w}
		name, eligible := staticSiteName(r.URL)
		if !eligible {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		serveStaticSiteFile(w, r, site, name)
	})
}

func staticSiteName(u *url.URL) (string, bool) {
	if u == nil || !strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\%") ||
		u.EscapedPath() != (&url.URL{Path: u.Path}).EscapedPath() {
		return "", false
	}
	if u.Path == "/" {
		return "index.html", true
	}
	relative := strings.TrimPrefix(u.Path, "/")
	if !fs.ValidPath(relative) {
		return "", false
	}
	for _, segment := range strings.Split(relative, "/") {
		if strings.HasPrefix(segment, ".") || strings.ContainsAny(segment, "\x00\r\n") {
			return "", false
		}
	}
	if strings.HasPrefix(relative, "assets/") {
		return relative, true
	}
	if (relative == "app" || strings.HasPrefix(relative, "app/")) && !strings.Contains(relative, ".") {
		return "index.html", true
	}
	return "", false
}

func serveStaticSiteFile(w http.ResponseWriter, r *http.Request, site fs.FS, name string) {
	file, err := site.Open(name) // Open exactly the selected name once; no shell retry.
	if err != nil {
		staticSiteError(w, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Cleanup cannot retract a published response; never append HTML.
			slog.Error("close static site file failed")
		}
	}()
	info, err := file.Stat()
	if err != nil {
		staticSiteError(w, err)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	content, ok := file.(io.ReadSeeker)
	if !ok {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if _, err := content.Seek(0, io.SeekEnd); err != nil {
		staticSiteError(w, err)
		return
	}
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		staticSiteError(w, err)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	// ServeContent owns HEAD/length and transfer semantics. Its later seek errors
	// are redacted, and read/write failures never trigger a second response.
	http.ServeContent(w, r, name, info.ModTime(), siteReadSeeker{content})
}

func staticSiteError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, fs.ErrNotExist) {
		status = http.StatusNotFound
	} else if errors.Is(err, fs.ErrPermission) {
		status = http.StatusForbidden
	}
	http.Error(w, http.StatusText(status), status)
}

// No buffering or status interception: reapply only this site's no-store header
// because ServeContent removes Cache-Control on its own error responses.
type siteResponseWriter struct{ http.ResponseWriter }

func (w siteResponseWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(status)
}
func (w siteResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type siteReadSeeker struct{ io.ReadSeeker }

func (s siteReadSeeker) Seek(offset int64, whence int) (int64, error) {
	position, err := s.ReadSeeker.Seek(offset, whence)
	if err != nil {
		return position, errors.New("static content unavailable")
	}
	return position, nil
}
