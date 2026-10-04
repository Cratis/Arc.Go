// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

type staticSiteHost struct {
	app     *arc.Application
	server  *httptest.Server
	client  *http.Client
	entered atomic.Int64
}

func newStaticSiteHost(t *testing.T, site fs.FS, options arc.Options, register func(*arc.Builder)) *staticSiteHost {
	t.Helper()
	builder, err := arc.NewBuilder(options)
	if err != nil {
		t.Fatal(err)
	}
	if register != nil {
		register(builder)
	}
	host := &staticSiteHost{}
	handler := newStaticSite(site)
	if err := builder.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.entered.Add(1)
		handler.ServeHTTP(w, r)
	})); err != nil {
		t.Fatal(err)
	}
	host.app, err = builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	host.server = httptest.NewServer(host.app)
	host.client = host.server.Client()
	host.client.Timeout = 5 * time.Second
	host.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(func() {
		host.client.CloseIdleConnections()
		serverCtx, cancelServer := context.WithTimeout(context.Background(), 5*time.Second)
		serverErr := host.server.Config.Shutdown(serverCtx)
		cancelServer()
		host.server.Close() // Join the real server's workers, even if Shutdown failed.
		appCtx, cancelApp := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelApp()
		if serverErr != nil {
			t.Error("external HTTP server shutdown:", serverErr)
		}
		if err := host.app.Shutdown(appCtx); err != nil {
			t.Error("Arc application shutdown:", err)
		}
	})
	return host
}

type staticSiteReply struct {
	status  int
	header  http.Header
	body    string
	readErr error
}

func (h *staticSiteHost) request(t *testing.T, method, target, credentials string, headers ...http.Header) staticSiteReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var bodyReader io.Reader
	if method == "QUERY" {
		bodyReader = strings.NewReader(`{"arguments":{}}`)
	}
	request, err := http.NewRequestWithContext(ctx, method, h.server.URL+target, bodyReader)
	if err != nil {
		t.Fatal(err)
	}
	if credentials != "" {
		request.Header.Set("Authorization", credentials)
	}
	for _, header := range headers {
		for key, values := range header {
			request.Header[key] = slices.Clone(values)
		}
	}
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return staticSiteReply{response.StatusCode, response.Header, string(body), readErr}
}

func embeddedStaticSite(t *testing.T) fs.FS {
	t.Helper()
	site, err := fs.Sub(staticSiteFiles, "testdata/staticfiles/site")
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func assertStaticSiteReply(t *testing.T, reply staticSiteReply, status int) {
	t.Helper()
	if reply.status != status || reply.readErr != nil {
		t.Fatalf("status=%d want=%d read=%v body=%q", reply.status, status, reply.readErr, reply.body)
	}
	if reply.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("site cache header=%q", reply.header.Get("Cache-Control"))
	}
	if status != http.StatusOK && (strings.Contains(reply.body, "Arc static shell") || strings.Contains(reply.body, "private failure")) {
		t.Fatalf("failure exposed shell or filesystem error: %q", reply.body)
	}
}

func TestStaticSiteActualAssetBytesMediaLengthAndEmptyHEAD(t *testing.T) {
	site := embeddedStaticSite(t)
	host := newStaticSiteHost(t, site, arc.Options{}, nil)
	for _, name := range []string{"assets/app.js", "assets/site.css"} {
		t.Run(name, func(t *testing.T) {
			want, err := fs.ReadFile(site, name)
			if err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "HEAD"} {
				reply := host.request(t, method, "/"+name, "")
				assertStaticSiteReply(t, reply, 200)
				wantBody := string(want)
				if method == "HEAD" {
					wantBody = ""
				}
				if reply.body != wantBody || reply.header.Get("Content-Length") != strconv.Itoa(len(want)) || reply.header.Get("Content-Type") != mime.TypeByExtension(path.Ext(name)) {
					t.Fatalf("%s: body=%q headers=%v", method, reply.body, reply.header)
				}
			}
		})
	}
}

func TestStaticSiteFixedShellRootAndNestedExtensionlessRoutes(t *testing.T) {
	site := embeddedStaticSite(t)
	want, err := fs.ReadFile(site, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	tracked := &staticWitnessFS{base: site}
	host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
	for _, target := range []string{"/", "/app", "/app/tasks", "/app/tasks/active?view=all"} {
		for _, method := range []string{"GET", "HEAD"} {
			reply := host.request(t, method, target, "")
			assertStaticSiteReply(t, reply, 200)
			body := string(want)
			if method == "HEAD" {
				body = ""
			}
			if reply.body != body || reply.header.Get("Content-Type") != "text/html; charset=utf-8" || reply.header.Get("Content-Length") != strconv.Itoa(len(want)) {
				t.Fatalf("%s %s body=%q headers=%v", method, target, reply.body, reply.header)
			}
		}
	}
	names, closes := tracked.snapshot()
	if len(names) != 8 || closes != 8 {
		t.Fatalf("opened=%v closed=%d", names, closes)
	}
	for _, name := range names {
		if name != "index.html" {
			t.Fatal("shell searched a route-derived file", name)
		}
	}
}

func TestStaticSiteMissingAssetAndIndexNeverFallBack(t *testing.T) {
	for _, target := range []string{"/assets/missing.js", "/", "/app/tasks"} {
		t.Run(target, func(t *testing.T) {
			tracked := &staticWitnessFS{openErr: fs.ErrNotExist}
			host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
			assertStaticSiteReply(t, host.request(t, "GET", target, ""), 404)
			names, closes := tracked.snapshot()
			want := "index.html"
			if strings.HasPrefix(target, "/assets/") {
				want = "assets/missing.js"
			}
			if !slices.Equal(names, []string{want}) || closes != 0 {
				t.Fatalf("opened=%v closed=%d", names, closes)
			}
		})
	}
}

func TestStaticSiteNarrowEligibilityAndMethodsDoNotOpenFiles(t *testing.T) {
	tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
	host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
	for _, target := range []string{"/assets/app.js", "/", "/app/tasks"} {
		for _, method := range []string{"POST", "QUERY", "OPTIONS", "DELETE"} {
			reply := host.request(t, method, target, "")
			assertStaticSiteReply(t, reply, 405)
			if reply.header.Get("Allow") != "GET, HEAD" {
				t.Fatal(method, target, reply.header)
			}
		}
	}
	for _, target := range []string{"/app/missing.js", "/unrelated", "/other.html", "/app/tasks.v1", "/assets", "/assets/", "/app/", "/assets/.secret", "/assets/private/.secret", "/api", "/api/unknown", "/API/unknown"} {
		for _, method := range []string{"GET", "POST"} {
			assertStaticSiteReply(t, host.request(t, method, target, ""), 404)
		}
	}
	if names, closes := tracked.snapshot(); len(names) != 0 || closes != 0 {
		t.Fatalf("ineligible/method rejection touched FS: %v %d", names, closes)
	}
}

func registerStaticSiteQuery(t *testing.T, builder *arc.Builder, name, target string, declaration metadata.Authorization, callback func(context.Context, queries.NoArguments) (siteMessage, error)) {
	t.Helper()
	if err := queries.Register[siteMessage](builder, name, queries.Function(callback),
		queries.WithPath[queries.NoArguments](target), queries.WithAuthorization[queries.NoArguments](declaration)); err != nil {
		t.Fatal(err)
	}
}

func TestStaticSiteArcQuerySuccessRedactedFailureDenialAnd405BypassSiteCallback(t *testing.T) {
	tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
	host := newStaticSiteHost(t, tracked, arc.Options{}, func(builder *arc.Builder) {
		for i, target := range []string{"/api/site-message", "/assets/app.js", "/app/tasks"} {
			registerStaticSiteQuery(t, builder, "Success"+strconv.Itoa(i), target, metadata.Authorization{AllowAnonymous: true},
				func(context.Context, queries.NoArguments) (siteMessage, error) {
					return siteMessage{Message: "Arc query"}, nil
				})
		}
		registerStaticSiteQuery(t, builder, "Failed", "/assets/failed.js", metadata.Authorization{AllowAnonymous: true},
			func(context.Context, queries.NoArguments) (siteMessage, error) {
				return siteMessage{}, errors.New("private failure")
			})
		registerStaticSiteQuery(t, builder, "Denied", "/assets/denied.js", metadata.Authorization{},
			func(context.Context, queries.NoArguments) (siteMessage, error) {
				return siteMessage{Message: "must not execute"}, nil
			})
	})
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{"GET", "/api/site-message", 200}, {"QUERY", "/api/site-message", 200},
		{"GET", "/assets/app.js", 200}, {"HEAD", "/assets/app.js", 200}, {"QUERY", "/assets/app.js", 200},
		{"GET", "/app/tasks", 200}, {"GET", "/assets/failed.js", 500}, {"GET", "/assets/denied.js", 403},
		{"POST", "/assets/app.js", 405}, {"OPTIONS", "/assets/app.js", 405}, {"DELETE", "/assets/app.js", 405},
	} {
		reply := host.request(t, tc.method, tc.target, "")
		wantCache := ""
		if tc.method == "QUERY" {
			wantCache = "no-store" // Native Arc QUERY behavior, not the site's header.
		}
		if reply.status != tc.status || reply.readErr != nil || reply.header.Get("Cache-Control") != wantCache || strings.Contains(reply.body, "Arc static shell") || strings.Contains(reply.body, "private failure") {
			t.Fatalf("%+v: %+v", tc, reply)
		}
		if tc.method == "GET" && tc.status == 200 && !strings.Contains(reply.body, "Arc query") {
			t.Fatal("query did not execute", reply.body)
		}
		if tc.status == 405 && reply.header.Get("Allow") != "GET, HEAD, QUERY" {
			t.Fatal(reply.header)
		}
	}
	if host.entered.Load() != 0 {
		t.Fatal("Arc route entered the site callback", host.entered.Load())
	}
	if names, _ := tracked.snapshot(); len(names) != 0 {
		t.Fatal("Arc route accessed FS", names)
	}
}

func TestStaticSiteReservedMappedUnmappedDisabledAndMixedCaseBypassSiteCallback(t *testing.T) {
	for _, development := range []bool{false, true} {
		for _, disabled := range []bool{false, true} {
			t.Run(strconv.FormatBool(development)+"/disabled="+strconv.FormatBool(disabled), func(t *testing.T) {
				options := arc.Options{}
				if development {
					options.Environment = "Development"
				}
				if disabled {
					enabled := false
					options.Introspection.Enabled = &enabled
				}
				tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
				host := newStaticSiteHost(t, tracked, options, nil)
				for _, target := range []string{"/.cratis/me", "/.cratis/commands", "/.cratis/queries", "/.cratis/unknown", "/.CRATIS/me", "/.CrAtIs/unknown", "/.cratis"} {
					reply := host.request(t, "GET", target, "")
					want := 404
					if target == "/.cratis/me" {
						want = 401
					} else if development && !disabled && (target == "/.cratis/commands" || target == "/.cratis/queries") {
						want = 200
					}
					if reply.status != want || reply.readErr != nil || strings.Contains(reply.body, "Arc static shell") || reply.header.Get("Cache-Control") == "no-store" {
						t.Fatal(target, reply)
					}
				}
				if host.entered.Load() != 0 {
					t.Fatal("reserved path entered site callback")
				}
				if names, _ := tracked.snapshot(); len(names) != 0 {
					t.Fatal(names)
				}
			})
		}
	}
}

func TestStaticSiteCanonicalRejectionsNeverAccessOutsideOrInsideFS(t *testing.T) {
	tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
	host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
	for _, target := range []string{"/assets/../index.html", "/assets/./app.js", "//assets/app.js", "/assets//app.js", "/assets/..\\outside.js", "/assets/%2e%2e/outside.js", "/assets/%2Foutside.js", "/assets/%5coutside.js", "/assets/%61pp.js", "/assets/%252e%252e/outside.js", "/assets/%252Foutside.js"} {
		t.Run(target, func(t *testing.T) {
			reply := host.request(t, "GET", target, "")
			want := 400 // Existing Arc canonical-path rejection is authoritative.
			if strings.Contains(target, "%25") {
				want = 404 // Never double-unescape the residual percent spelling.
			}
			if reply.status != want || reply.readErr != nil || reply.header.Get("Location") != "" || strings.Contains(reply.body, "Arc static shell") {
				t.Fatal(reply)
			}
		})
	}
	if names, closes := tracked.snapshot(); len(names) != 0 || closes != 0 {
		t.Fatalf("unsafe path opened FS names=%v closed=%d", names, closes)
	}
}

func TestStaticSitePrepublicationFailuresAndDirectoriesCloseWithoutErrorHTML(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*staticWitnessFS)
		status int
		closed int
	}{
		{"open permission", func(f *staticWitnessFS) { f.openErr = fs.ErrPermission }, 403, 0},
		{"open failure", func(f *staticWitnessFS) { f.openErr = errors.New("private failure") }, 500, 0},
		{"stat missing", func(f *staticWitnessFS) { f.statErr = fs.ErrNotExist }, 404, 1},
		{"stat permission", func(f *staticWitnessFS) { f.statErr = fs.ErrPermission }, 403, 1},
		{"stat failure", func(f *staticWitnessFS) { f.statErr = errors.New("private failure") }, 500, 1},
		{"seek permission", func(f *staticWitnessFS) { f.seekErr = fs.ErrPermission }, 403, 1},
		{"seek failure", func(f *staticWitnessFS) { f.seekErr = errors.New("private failure") }, 500, 1},
		{"not seekable", func(f *staticWitnessFS) { f.notSeekable = true }, 500, 1},
		{"directory", func(f *staticWitnessFS) { f.directory = true }, 404, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
			tc.setup(tracked)
			host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
			reply := host.request(t, "GET", "/assets/app.js", "")
			assertStaticSiteReply(t, reply, tc.status)
			if strings.HasPrefix(reply.header.Get("Content-Type"), "text/html") {
				t.Fatal("error HTML", reply)
			}
			names, closes := tracked.snapshot()
			if !slices.Equal(names, []string{"assets/app.js"}) || closes != tc.closed {
				t.Fatalf("opened=%v closed=%d", names, closes)
			}
		})
	}
}

func TestStaticSiteReadAndCloseFailureNeverAppendShellOrRetractPublication(t *testing.T) {
	for _, readFailure := range []bool{true, false} {
		t.Run("read="+strconv.FormatBool(readFailure), func(t *testing.T) {
			tracked := &staticWitnessFS{base: embeddedStaticSite(t), readFailure: readFailure, closeFailure: !readFailure}
			host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
			reply := host.request(t, "GET", "/assets/app.js", "")
			if reply.status != 200 || strings.Contains(reply.body, "Arc static shell</") || strings.Contains(reply.body, "private failure") || strings.HasPrefix(reply.header.Get("Content-Type"), "text/html") {
				t.Fatal(reply)
			}
			if readFailure && !errors.Is(reply.readErr, io.ErrUnexpectedEOF) {
				t.Fatalf("published truncated read not surfaced to client: %v", reply.readErr)
			}
			if !readFailure && reply.readErr != nil {
				t.Fatal(reply.readErr)
			}
			names, closes := tracked.snapshot()
			if len(names) != 1 || closes != 1 {
				t.Fatal(names, closes)
			}
		})
	}
}

func TestStaticSiteAnonymousPublicContentTerminalCredentialsAndNoImplicitRawAuthorization(t *testing.T) {
	fallback := metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"SiteReader"}}}}
	tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
	var rescueCalls atomic.Int64
	host := newStaticSiteHost(t, tracked, arc.Options{
		Authorization: authorization.Options{Fallback: &fallback},
		Authentication: []authentication.Handler{
			authentication.HandlerFunc(func(_ context.Context, request *http.Request) (authentication.Result, error) {
				if request.Header.Get("Authorization") != "" {
					return authentication.Failed("private failure"), nil
				}
				return authentication.Anonymous(), nil
			}),
			authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
				rescueCalls.Add(1)
				return authentication.Anonymous(), nil
			}),
		},
	}, func(builder *arc.Builder) {
		registerStaticSiteQuery(t, builder, "Private", "/api/private", metadata.Authorization{},
			func(context.Context, queries.NoArguments) (siteMessage, error) {
				return siteMessage{}, errors.New("must not execute")
			})
	})
	assertStaticSiteReply(t, host.request(t, "GET", "/assets/site.css", ""), 200)
	before := host.entered.Load()
	namesBefore, _ := tracked.snapshot()
	rescuesBefore := rescueCalls.Load()
	reply := host.request(t, "GET", "/assets/site.css", "invalid test credentials")
	if reply.status != 401 || reply.readErr != nil || reply.body != "" || reply.header.Get("Cache-Control") == "no-store" || host.entered.Load() != before || rescueCalls.Load() != rescuesBefore {
		t.Fatal("terminal rejection reached site or rescue", reply)
	}
	if names, _ := tracked.snapshot(); !slices.Equal(names, namesBefore) {
		t.Fatal("credential rejection touched FS", names)
	}
	reply = host.request(t, "GET", "/api/private", "")
	if reply.status != 403 || reply.header.Get("Cache-Control") == "no-store" || host.entered.Load() != before {
		t.Fatal("query authorization/cache isolation", reply)
	}
}

func TestStaticSiteStartupConcurrentRequestsAndJoinedShutdown(t *testing.T) {
	tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
	host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
	if err := host.app.Start(t.Context()); err != nil {
		t.Fatal("repeated Start", err)
	}
	const requests = 12
	var workers sync.WaitGroup
	for range requests {
		workers.Go(func() {
			reply := host.request(t, "GET", "/assets/site.css", "")
			assertStaticSiteReply(t, reply, 200)
		})
	}
	workers.Wait() // Every request is bounded; join before test cleanup stops hosts.
	names, closes := tracked.snapshot()
	if len(names) != requests || closes != requests {
		t.Fatal("request resources not joined", names, closes)
	}
}

func TestStaticSiteServeContentRangeErrorsRetainSiteOnlyNoStore(t *testing.T) {
	tracked := &staticWitnessFS{base: embeddedStaticSite(t)}
	host := newStaticSiteHost(t, tracked, arc.Options{}, nil)
	for _, method := range []string{"GET", "HEAD"} {
		reply := host.request(t, method, "/assets/app.js", "", http.Header{"Range": {"bytes=999999-"}})
		assertStaticSiteReply(t, reply, 416)
	}
	names, closes := tracked.snapshot()
	if len(names) != 2 || closes != 2 {
		t.Fatal(names, closes)
	}
}

func TestStaticSiteHostingExcerptsMatchCompiledExampleAndLocalLinks(t *testing.T) {
	document, err := os.ReadFile("Documentation/backend/go/core/hosting.md")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("static_files_example_test.go")
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(text string) string {
		lines := strings.Split(text, "\n")
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		return strings.Join(lines, "\n")
	}
	_, section, found := strings.Cut(string(document), "## Serve an embedded public site")
	if !found {
		t.Fatal("missing static-site section")
	}
	section, _, found = strings.Cut(section, "## Boundaries")
	if !found {
		t.Fatal("missing following hosting section")
	}
	count := 0
	for _, piece := range strings.Split(section, "```go\n")[1:] {
		block, _, found := strings.Cut(piece, "\n```")
		if !found || !strings.Contains(normalize(string(source)), normalize(block)) {
			t.Fatalf("unmatched Go excerpt: %s", block)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("checked %d excerpts, want 2", count)
	}
	for _, piece := range strings.Split(section, "](")[1:] {
		target, _, _ := strings.Cut(piece, ")")
		if strings.Contains(target, ":") || strings.HasPrefix(target, "#") {
			continue
		}
		if _, err := os.Stat(path.Join("Documentation/backend/go/core", target)); err != nil {
			t.Fatalf("broken local link %q: %v", target, err)
		}
	}
}

// staticWitnessFS records every access, including attempted outside names. It
// injects failures without relying on platform permissions or mutable symlinks.
type staticWitnessFS struct {
	base         fs.FS
	openErr      error
	statErr      error
	seekErr      error
	notSeekable  bool
	directory    bool
	readFailure  bool
	closeFailure bool
	mu           sync.Mutex
	names        []string
	closes       int
}

func (f *staticWitnessFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	f.names = append(f.names, name)
	f.mu.Unlock()
	if f.openErr != nil {
		return nil, &fs.PathError{Op: "open", Path: "private failure", Err: f.openErr}
	}
	file, err := f.base.Open(name)
	if err != nil {
		return nil, err
	}
	tracked := &staticWitnessFile{File: file, owner: f}
	if f.notSeekable {
		return tracked, nil
	}
	return &staticWitnessSeekFile{staticWitnessFile: tracked, seeker: file.(io.ReadSeeker)}, nil
}
func (f *staticWitnessFS) snapshot() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.names), f.closes
}

type staticWitnessFile struct {
	fs.File
	owner *staticWitnessFS
}

func (f *staticWitnessFile) Stat() (fs.FileInfo, error) {
	if f.owner.statErr != nil {
		return nil, &fs.PathError{Op: "stat", Path: "private failure", Err: f.owner.statErr}
	}
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	if f.owner.directory {
		return staticWitnessDirectory{info}, nil
	}
	return info, nil
}
func (f *staticWitnessFile) Read(data []byte) (int, error) {
	if f.owner.readFailure {
		return 0, errors.New("private failure")
	}
	return f.File.Read(data)
}
func (f *staticWitnessFile) Close() error {
	f.owner.mu.Lock()
	f.owner.closes++
	f.owner.mu.Unlock()
	err := f.File.Close()
	if f.owner.closeFailure {
		return errors.Join(err, errors.New("private failure"))
	}
	return err
}

type staticWitnessSeekFile struct {
	*staticWitnessFile
	seeker io.ReadSeeker
}

func (f *staticWitnessSeekFile) Seek(offset int64, whence int) (int64, error) {
	if f.owner.seekErr != nil {
		return 0, &fs.PathError{Op: "seek", Path: "private failure", Err: f.owner.seekErr}
	}
	return f.seeker.Seek(offset, whence)
}

type staticWitnessDirectory struct{ fs.FileInfo }

func (staticWitnessDirectory) Mode() fs.FileMode { return fs.ModeDir | 0o555 }
func (staticWitnessDirectory) IsDir() bool       { return true }
