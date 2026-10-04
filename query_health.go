// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

const queryHealthPath = "/.cratis/queries/health"

// QueryHealthName is the protected framework query's backend and hub identity.
const QueryHealthName queries.FullyQualifiedQueryName = "QueryHealth.ObserveHealth"

// QueryHealthOptions explicitly enables aggregate health. Nil Options.QueryHealth
// exposes nothing, including in Development. Roles must contain at least one
// nonempty role; configuration is copied and any one authenticated role suffices.
type QueryHealthOptions struct {
	// Roles is the administrative allowlist; any authenticated matching role grants access.
	Roles []string
}

// QueryHealth is an aggregate native-owner sample. Application observations exclude
// this health query itself. Hub operations and observations overlap and must not be
// added together. Individual owner samples are synchronized, not globally atomic.
// No IDs, principal/client data, arguments, cached results or errors are included.
type QueryHealth struct {
	// Observations groups currently owned application observations by bounded label.
	Observations []queries.ObservationHealth `json:"observations"`
	// Hubs groups currently owned physical hub connections by fixed protocol.
	Hubs []QueryHealthHub `json:"hubs"`
	// Openings counts hub subscriptions still in admission/source opening.
	Openings int `json:"openings"`
	// Operations includes active and retired-but-unjoined hub subscriptions.
	Operations int `json:"operations"`
}

// QueryHealthHub contains physical connection counts, not subscription counts.
type QueryHealthHub struct {
	// Protocol is sse or websocket.
	Protocol string `json:"protocol"`
	// Connected counts connections without a cancellation signal.
	Connected int `json:"connected"`
	// Closing counts canceled connections still retained for joining.
	Closing int `json:"closing"`
}

func copyQueryHealthOptions(options *QueryHealthOptions) (*QueryHealthOptions, error) {
	if options == nil {
		return nil, nil
	}
	copy := &QueryHealthOptions{Roles: slices.Clone(options.Roles)}
	if len(copy.Roles) == 0 {
		return nil, ErrInvalidOptions
	}
	for _, role := range copy.Roles {
		if strings.TrimSpace(role) == "" || strings.TrimSpace(role) != role || len(role) > 512 {
			return nil, ErrInvalidOptions
		}
	}
	return copy, nil
}

// A private pipeline isolates framework health from application resource openers,
// validators, policies, filters and factories. The facade gates every entry before
// any private-pipeline activation; its own declaration rechecks each emission.
type healthQueries struct {
	application queries.Pipeline
	framework   queries.ObservablePipeline
	roles       []string
	recorder    *observability.Recorder
}

func (a *Application) initQueryHealth() error {
	if a.options.QueryHealth == nil {
		return nil
	}
	if _, exists := a.queries.Lookup(QueryHealthName); exists {
		return ErrRouteConflict
	}
	facade := &healthQueries{application: a.queries, roles: slices.Clone(a.options.QueryHealth.Roles), recorder: a.options.Diagnostics}
	var registry queries.Registry
	err := queries.RegisterObservable[QueryHealth](&registry, "ObserveHealth", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[QueryHealth], error) {
		return &healthSource{a: a, pipeline: facade.application, roles: facade.roles}, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: facade.roles}}}), queries.WithExcludeFromDiscovery[queries.NoArguments](true), queries.WithPath[queries.NoArguments](queryHealthPath))
	if err != nil {
		return err
	}
	pipeline, err := registry.Build(queries.PipelineOptions{Diagnostics: a.options.Diagnostics, MaximumWait: a.options.Observable.MaximumWait, MaxObservations: a.options.Observable.MaxObservations, ObservationCleanupTimeout: a.options.Observable.CloseGrace})
	if err != nil {
		return err
	}
	facade.framework = pipeline.(queries.ObservablePipeline)
	a.queries = facade
	return nil
}

func healthAllowed(ctx context.Context, roles []string) bool {
	if ctx == nil {
		return false
	}
	principal, _ := identity.PrincipalFrom(ctx)
	for _, role := range roles {
		if principal.HasRole(role) {
			return true
		}
	}
	return false
}
func healthDenied(ctx context.Context) queries.Result[any] {
	return queries.Unauthorized[any](contextID(ctx), "")
}
func (p *healthQueries) Diagnostics() *observability.Recorder { return p.recorder }
func (p *healthQueries) Lookup(name queries.FullyQualifiedQueryName) (queries.Registration, bool) {
	if name == QueryHealthName {
		return p.framework.Lookup(name)
	}
	return p.application.Lookup(name)
}
func (p *healthQueries) Perform(ctx context.Context, name queries.FullyQualifiedQueryName, request queries.Request) (queries.Result[any], error) {
	if name != QueryHealthName {
		return p.application.Perform(ctx, name, request)
	}
	if !healthAllowed(ctx, p.roles) {
		return healthDenied(ctx), nil
	}
	return p.framework.Perform(ctx, name, request)
}
func (p *healthQueries) PerformScoped(ctx context.Context, scope *execution.Scope, name queries.FullyQualifiedQueryName, request queries.Request) (queries.Result[any], error) {
	if name != QueryHealthName {
		return p.application.PerformScoped(ctx, scope, name, request)
	}
	if !healthAllowed(ctx, p.roles) {
		return healthDenied(ctx), nil
	}
	// The caller's scope is deliberately not borrowed: it may own application
	// factories or participants. Health uses only its isolated framework scope.
	if scope == nil {
		return queries.FromError[any](contextID(ctx), execution.ErrInvalidScope), execution.ErrInvalidScope
	}
	if err := scope.CheckContext(ctx); err != nil {
		return queries.FromError[any](contextID(ctx), err), err
	}
	return p.framework.Perform(ctx, name, request)
}
func (p *healthQueries) Open(ctx context.Context, name queries.FullyQualifiedQueryName, request queries.Request) (*queries.Observation, queries.Result[any], error) {
	if name == QueryHealthName {
		if !healthAllowed(ctx, p.roles) {
			return nil, healthDenied(ctx), nil
		}
		return p.framework.Open(ctx, name, request)
	}
	return p.application.(queries.ObservablePipeline).Open(ctx, name, request)
}
func (p *healthQueries) CloseObservations(ctx context.Context) error {
	// Each native owner retains timed-out cleanup for subsequent shutdown calls.
	return errors.Join(p.application.(queries.ObservablePipeline).CloseObservations(ctx), p.framework.CloseObservations(ctx))
}

type healthSource struct {
	a        *Application
	pipeline queries.Pipeline
	roles    []string
}

func (s *healthSource) Current(ctx context.Context) (QueryHealth, bool, error) {
	if err := ctx.Err(); err != nil {
		return QueryHealth{}, false, err
	}
	if !healthAllowed(ctx, s.roles) {
		return QueryHealth{}, false, execution.ErrIdentityChanged
	}
	result := QueryHealth{Observations: []queries.ObservationHealth{}, Hubs: []QueryHealthHub{{Protocol: "sse"}, {Protocol: "websocket"}}}
	if reporter, ok := s.pipeline.(queries.HealthReporter); ok {
		result.Observations = reporter.QueryHealth()
	}
	s.a.hubs.mu.Lock()
	result.Openings, result.Operations = s.a.hubs.openings, s.a.hubs.operations
	for _, connection := range s.a.hubs.connections {
		index := 0
		if connection.websocket {
			index = 1
		}
		if connection.ctx.Err() != nil {
			result.Hubs[index].Closing++
		} else {
			result.Hubs[index].Connected++
		}
	}
	s.a.hubs.mu.Unlock()
	return result, true, nil
}
func (s *healthSource) Open(ctx context.Context) (observable.Stream[QueryHealth], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !healthAllowed(ctx, s.roles) {
		return nil, execution.ErrIdentityChanged
	}
	return &healthStream{source: s, initial: true}, nil
}

type healthStream struct {
	source  *healthSource
	initial bool
}

func (s *healthStream) Next(ctx context.Context) (QueryHealth, error) {
	if !s.initial {
		// No publisher worker: the consuming owner waits synchronously and owns
		// the timer. Fixed one-second sampling bounds work without configuration.
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return QueryHealth{}, ctx.Err()
		case <-timer.C:
		}
	}
	s.initial = false
	result, _, err := s.source.Current(ctx)
	return result, err
}
func (*healthStream) Close(context.Context) error { return nil }
