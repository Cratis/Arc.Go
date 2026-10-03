// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
)

// Builder is single-owner composition state, not concurrent-safe. Build is a
// single attempt: success is cached, failure is terminal. Borrowed registries
// must not be built independently or mutated after the root Build attempt.
type Builder struct {
	options            Options
	commands           *commands.Registry
	queries            *queries.Registry
	policies           authorization.Registry
	validators         validation.Registry
	attempted          bool
	application        *Application
	buildErr           error
	rawHandlers        []rawHandler
	details            []detailsRegistration
	schemas            map[reflect.Type]json.RawMessage
	users              []listProvider[UsersProvider]
	tenants            []listProvider[TenantsProvider]
	hooks              []lifecycleEntry
	middleware         []Middleware
	generatedContracts []generatedContract
}

// NewBuilder validates and copies configuration without activation or I/O.
func NewBuilder(options Options) (*Builder, error) {
	o, err := normalizeOptions(options)
	if err != nil {
		return nil, &ConfigurationError{Component: "options", Cause: err}
	}
	c, err := commands.NewRegistry(commands.RegistryOptions{Namespace: o.Namespace})
	if err != nil {
		return nil, err
	}
	q, err := queries.NewRegistry(queries.RegistryOptions{Namespace: o.Namespace})
	if err != nil {
		return nil, err
	}
	return &Builder{options: o, commands: c, queries: q}, nil
}

// RegisterCommand implements the generated/manual command registrar seam.
func (b *Builder) RegisterCommand(r commands.Registration) error {
	if b.attempted {
		return ErrFrozen
	}
	return b.commands.RegisterCommand(r)
}

// RegisterReadModel implements the query registrar seam.
func (b *Builder) RegisterReadModel(r queries.ModelRegistration) error {
	if b.attempted {
		return ErrFrozen
	}
	return b.queries.RegisterReadModel(r)
}

// RegisterQuery implements the query registrar seam.
func (b *Builder) RegisterQuery(r queries.Registration) error {
	if b.attempted {
		return ErrFrozen
	}
	return b.queries.RegisterQuery(r)
}

// Commands borrows the construction registry; do not Build it independently.
func (b *Builder) Commands() *commands.Registry { return b.commands }

// Queries borrows the construction registry; do not Build it independently.
func (b *Builder) Queries() *queries.Registry { return b.queries }

// Policies borrows the construction registry; do not Build it independently.
func (b *Builder) Policies() *authorization.Registry { return &b.policies }

// Validators borrows the construction registry; do not Build it independently.
func (b *Builder) Validators() *validation.Registry { return &b.validators }

// Catalog returns an independent combined declaration snapshot.
func (b *Builder) Catalog() metadata.Catalog {
	c := b.commands.Catalog()
	c.Queries = b.queries.Catalog().Queries
	return c
}

// Build seals composition, validates metadata, and constructs frozen pipelines.
// No application provider, dependency, listener or business callback is activated.
func (b *Builder) Build() (*Application, error) {
	if b.attempted {
		return b.application, b.buildErr
	}
	b.attempted = true
	b.application, b.buildErr = b.build()
	if b.buildErr != nil {
		b.buildErr = &ConfigurationError{Component: "build", Cause: b.buildErr}
	}
	return b.application, b.buildErr
}
func (b *Builder) build() (*Application, error) {
	details, err := b.selectDetails()
	if err != nil {
		return nil, err
	}
	catalog := b.Catalog()
	endpoints, err := metadata.Resolve(catalog, *b.options.Routes)
	if err != nil {
		return nil, err
	}
	for _, contract := range b.generatedContracts {
		if err := metadata.VerifyGeneratedEndpoints(contract.profile, contract.endpoints, endpoints); err != nil {
			return nil, err
		}
	}
	evaluator, err := b.policies.Build(catalog, b.options.Authorization)
	if err != nil {
		return nil, err
	}
	graph, err := b.validators.Build()
	if err != nil {
		return nil, err
	}
	o := b.options
	pipelineOpener := o.OpenResources
	if o.ScopeFactory != nil {
		pipelineOpener = nil
	}
	cp, err := b.commands.Build(commands.PipelineOptions{ScopeFactory: o.ScopeFactory, OpenResources: pipelineOpener, DependencyCatalog: o.DependencyCatalog, Authorization: evaluator, Validation: graph, Membership: o.Membership, RequireTenant: o.RequireTenant, Clock: o.Clock, CleanupTimeout: o.CleanupTimeout, ExposeExceptionDetails: o.ExposeExceptionDetails, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	qp, err := b.queries.Build(queries.PipelineOptions{ScopeFactory: o.ScopeFactory, OpenResources: pipelineOpener, DependencyCatalog: o.DependencyCatalog, Authorization: evaluator, Validation: graph, Membership: o.Membership, RequireTenant: o.RequireTenant, Clock: o.Clock, CleanupTimeout: o.CleanupTimeout, ExposeExceptionDetails: o.ExposeExceptionDetails, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	for _, p := range b.users {
		if err := checkProviderKeys(o.DependencyCatalog, p.keys); err != nil {
			return nil, err
		}
	}
	for _, p := range b.tenants {
		if err := checkProviderKeys(o.DependencyCatalog, p.keys); err != nil {
			return nil, err
		}
	}
	a := &Application{schemas: b.schemas, users: slices.Clone(b.users), tenants: slices.Clone(b.tenants), details: details, options: o, catalog: cloneCatalog(catalog), endpoints: slices.Clone(endpoints), commands: cp, queries: qp}
	if err := a.compileReaders(); err != nil {
		return nil, err
	}
	if err := a.compileRoutes(b.rawHandlers); err != nil {
		return nil, err
	}
	a.initLifetime(b.hooks)
	if err := a.composeMiddleware(b.middleware); err != nil {
		return nil, err
	}
	if err := a.compileCatalogs(); err != nil {
		return nil, err
	}
	return a, nil
}
func cloneCatalog(c metadata.Catalog) metadata.Catalog {
	// Metadata contains only framework-controlled scalar data, never custom codecs.
	body, _ := json.Marshal(c)
	var copy metadata.Catalog
	_ = json.Unmarshal(body, &copy)
	return copy
}
func nilValue(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan, reflect.Interface:
		return value.IsNil()
	}
	return false
}
