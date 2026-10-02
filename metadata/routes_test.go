// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metadata_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func TestConventionalRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, command string
		skip                     int
		prefix, path             string
	}{
		{"default", "Acme.Tasks.Registration", "RegisterTask", 0, "api", "/api/acme/tasks/registration/register-task"},
		{"skipped root", "Acme.Tasks.Registration", "RegisterTask", 1, "api", "/api/tasks/registration/register-task"},
		{"acronyms", "Acme.HTTP_API", "GetURL", 1, "api", "/api/h-t-t-p-a-p-i/get-u-r-l"},
		{"underscore", "", "Create_Task", 0, "api", "/api/create-task"},
		{"prefix", "Acme.Tasks", "Create", 1, "/V2//API/", "/v2/api/tasks/create"},
		{"all skipped", "Acme.Tasks", "Create", 99, "", "/create"},
		{"global namespace", "", "Create", 0, "api", "/api/create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := metadata.Command{Type: metadata.TypeName{Namespace: tc.namespace, Name: tc.command}}
			options := metadata.DefaultOptions()
			options.SegmentsToSkip = tc.skip
			options.RoutePrefix = tc.prefix
			routes, err := metadata.Resolve(metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{c}}, options)
			if err != nil {
				t.Fatal(err)
			}
			want := []metadata.Endpoint{{Identity: c.Type.Identity(), Method: "POST", Path: tc.path}, {Identity: c.Type.Identity(), Method: "POST", Path: tc.path + "/validate", ValidateOnly: true}}
			if !reflect.DeepEqual(routes, want) {
				t.Fatalf("routes = %#v, want %#v", routes, want)
			}
		})
	}
}

func TestQueryIdentityAndPathPrecedence(t *testing.T) {
	method := "/Exact/METHOD/"
	empty := ""
	for _, tc := range []struct {
		name        string
		method      *string
		model, path string
	}{
		{"convention", nil, "", "/api/tasks/listing/all"},
		{"model", nil, "/Exact/Model", "/Exact/Model"},
		{"method", &method, "/ignored", "/Exact/METHOD/"},
		{"empty method disables model", &empty, "/ignored", "/api/tasks/listing/all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := metadata.Query{ReadModel: metadata.TypeName{Namespace: "Acme.Tasks.Listing", Name: "Task"}, Name: "All", Path: tc.method, ReadModelPath: tc.model, Observable: true}
			options := metadata.DefaultOptions()
			options.SegmentsToSkip = 1
			routes, err := metadata.Resolve(metadata.Catalog{Version: 1, Queries: []metadata.Query{q}}, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(routes) != 2 || routes[0].Identity != "Acme.Tasks.Listing.Task.All" || routes[0].Path != tc.path || routes[1].Method != "QUERY" {
				t.Fatalf("routes = %#v", routes)
			}
		})
	}
}

func TestNamespaceConflictsRestoreNames(t *testing.T) {
	options := metadata.DefaultOptions()
	options.IncludeCommandName = false
	options.IncludeQueryName = false
	options.EnableQueryHTTPMethod = false
	options.SegmentsToSkip = 1
	first := metadata.Command{Type: metadata.TypeName{Namespace: "Acme.Tasks", Name: "Create"}}
	catalog := metadata.Catalog{Version: 1, Commands: []metadata.Command{first}}
	routes, err := metadata.Resolve(catalog, options)
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Path != "/api/tasks" {
		t.Fatalf("route = %s", routes[0].Path)
	}
	// Conflicts are grouped AFTER skipping. Custom routes still count, as in C#.
	catalog.Commands = append(catalog.Commands, metadata.Command{Type: metadata.TypeName{Namespace: "Other.Tasks", Name: "Delete"}, Path: "/explicit"})
	routes, err = metadata.Resolve(catalog, options)
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Path != "/api/tasks/create" {
		t.Fatalf("route = %s", routes[0].Path)
	}
	catalog = metadata.Catalog{Version: 1, Queries: []metadata.Query{
		{ReadModel: metadata.TypeName{Namespace: "Acme.Tasks", Name: "Task"}, Name: "All"},
		{ReadModel: metadata.TypeName{Namespace: "Acme.Tasks", Name: "Task"}, Name: "Active"},
	}}
	routes, err = metadata.Resolve(catalog, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Path != "/api/tasks/active" || routes[1].Path != "/api/tasks/all" {
		t.Fatalf("routes = %#v", routes)
	}
}

func TestCollisionsAreDeterministicAndInspectable(t *testing.T) {
	first := metadata.Command{Type: metadata.TypeName{Name: "First"}, Path: "/Route"}
	second := metadata.Command{Type: metadata.TypeName{Name: "Second"}, Path: "/route/validate"}
	catalog := metadata.Catalog{Version: 1, Commands: []metadata.Command{second, first}}
	_, err := metadata.Resolve(catalog, metadata.DefaultOptions())
	var collision *metadata.CollisionError
	if !errors.As(err, &collision) || collision.Key != "POST /route/validate" || collision.First != "First" || collision.Second != "Second" {
		t.Fatalf("error = %v", err)
	}
	catalog.Commands = []metadata.Command{first, second}
	_, reversed := metadata.Resolve(catalog, metadata.DefaultOptions())
	if err.Error() != reversed.Error() {
		t.Fatalf("nondeterministic errors: %v / %v", err, reversed)
	}
	catalog.Commands = []metadata.Command{first, first}
	if _, err = metadata.Resolve(catalog, metadata.DefaultOptions()); !errors.As(err, &collision) {
		t.Fatalf("duplicate identity = %v", err)
	}
}

func TestCommandAndQueryMaySharePath(t *testing.T) {
	path := "/same"
	catalog := metadata.Catalog{Version: 1, Commands: []metadata.Command{{Type: metadata.TypeName{Name: "Create"}, Path: path}}, Queries: []metadata.Query{{ReadModel: metadata.TypeName{Name: "Task"}, Name: "All", Path: &path}}}
	if routes, err := metadata.Resolve(catalog, metadata.DefaultOptions()); err != nil || len(routes) != 4 {
		t.Fatalf("routes = %#v, error = %v", routes, err)
	}
}

func TestInvalidMetadataFailsExplicitly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		catalog metadata.Catalog
		options metadata.Options
	}{
		{"version", metadata.Catalog{}, metadata.DefaultOptions()},
		{"negative skip", metadata.Catalog{Version: 1}, metadata.Options{SegmentsToSkip: -1}},
		{"empty identity", metadata.Catalog{Version: 1, Commands: []metadata.Command{{}}}, metadata.DefaultOptions()},
		{"bad namespace", metadata.Catalog{Version: 1, Commands: []metadata.Command{{Type: metadata.TypeName{Namespace: "Bad..Name", Name: "Create"}}}}, metadata.DefaultOptions()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := metadata.Resolve(tc.catalog, tc.options); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	for _, path := range []string{"relative", "/wild/{id}", "/a?b", "/a#b", "/a%20b", "/a/../b", "/a//b", "/white space", "/a\\b"} {
		t.Run(path, func(t *testing.T) {
			_, err := metadata.Resolve(metadata.Catalog{Version: 1, Commands: []metadata.Command{{Type: metadata.TypeName{Name: "Create"}, Path: path}}}, metadata.DefaultOptions())
			if err == nil {
				t.Fatal("expected unsupported path error")
			}
		})
	}
}
