// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

// Application is immutable compiled composition with explicit lifecycle. It must
// be constructed with Builder.Build; borrowed callbacks must support concurrency.
type Application struct {
	options        Options
	catalog        metadata.Catalog
	endpoints      []metadata.Endpoint
	commands       commands.Pipeline
	queries        queries.Pipeline
	handler        http.Handler
	discovery      discoveryMode
	routeTable     map[string]map[string]metadata.Endpoint
	readers        map[string]queries.RequestReader
	details        detailsRegistration
	schemas        map[reflect.Type]json.RawMessage
	catalogJSON    map[string]json.RawMessage
	users          []listProvider[UsersProvider]
	tenants        []listProvider[TenantsProvider]
	life           lifetime
	server         serverState
	rawMux         *http.ServeMux
	requestHandler http.Handler
	authentication *authentication.Chain
}

// Catalog returns copied declarations, including artifacts excluded from discovery.
func (a *Application) Catalog() metadata.Catalog { return cloneCatalog(a.catalog) }

// Endpoints returns a copied compiled Arc route table.
func (a *Application) Endpoints() []metadata.Endpoint { return slices.Clone(a.endpoints) }

// Commands exposes the backend command substitution seam.
func (a *Application) Commands() commands.Pipeline { return admittedCommands{a} }

// Queries exposes the backend snapshot query substitution seam.
func (a *Application) Queries() queries.Pipeline { return admittedQueries{a} }

// ServeHTTP requires explicit application startup before accepting work.
func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.serveObserved(w, r)
}
