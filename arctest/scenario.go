// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package arctest runs command and snapshot query scenarios through real Arc
// pipelines without HTTP. Configure a normal arc.Builder with explicit test
// dependencies, then call New. No discovery, container, or assertion library is
// required. HTTP binding, authentication middleware, and streaming are not tested
// by these scenarios; use an HTTP contract test for those boundaries.
package arctest

import (
	"context"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
)

// Scenario owns one started application and its lifecycle. Construct it with New;
// command and query scenarios borrow it. Concurrent execution follows the normal
// application contract: registered dependencies must support concurrent use.
// Values returned by pipelines are borrowed, not deep-copied by this test kit.
type Scenario struct {
	application *arc.Application
}

// New builds and starts builder, failing tb on configuration/startup errors, and
// registers bounded application shutdown with tb.Cleanup. Configure all service
// replacements, validators, policies and lifecycle hooks before calling New.
// The builder is sealed by Build. No listener or HTTP server is opened.
func New(tb testing.TB, builder *arc.Builder) *Scenario {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), 5*time.Second)
	defer cancel()
	s, err := NewScenario(ctx, builder)
	if err != nil {
		tb.Fatalf("arctest: initialize scenario: %v", err)
		return nil
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			tb.Errorf("arctest: shutdown application: %v", err)
		}
	})
	return s
}

// NewScenario builds and starts a scenario without a testing.TB. The caller owns
// Close; New is the test-cleanup convenience wrapper. ctx bounds startup only.
// Configuration/startup errors retain their identities; nil arguments return
// arc.ErrInvalidOptions. A failed startup performs Arc's lifecycle rollback.
func NewScenario(ctx context.Context, builder *arc.Builder) (*Scenario, error) {
	if ctx == nil || builder == nil {
		return nil, arc.ErrInvalidOptions
	}
	application, err := builder.Build()
	if err != nil {
		return nil, err
	}
	if err := application.Start(ctx); err != nil {
		return nil, err
	}
	return &Scenario{application: application}, nil
}

// Close stops the owned application using ctx as its cleanup budget. Repeated
// calls follow Application.Shutdown semantics. Subsequent operations fail with
// arc.ErrStopped. New also registers Close with the owning test's cleanup.
func (s *Scenario) Close(ctx context.Context) error { return s.application.Shutdown(ctx) }

// CommandScenario executes a registered command through authorization, validation,
// preparation, handling and completion. It does not turn pipeline errors into
// test failures; callers can inspect them with errors.Is/errors.As.
type CommandScenario[C, R any] struct {
	scenario *Scenario
}

// NewCommand selects the registered command C and expected response R. It borrows
// a non-nil Scenario made by New. Use commands.NoResponse for void commands.
func NewCommand[C, R any](scenario *Scenario) *CommandScenario[C, R] {
	return &CommandScenario[C, R]{scenario: scenario}
}

// Execute runs the real pipeline synchronously. ctx supplies cancellation and
// trusted identity/tenant/correlation metadata, just as in backend execution.
// Typed values are passed directly; HTTP JSON binding is intentionally not run.
func (s *CommandScenario[C, R]) Execute(ctx context.Context, value C) (commands.Result[R], error) {
	return commands.Execute[R](ctx, s.scenario.application.Commands(), value)
}

// Validate runs authorization and validation without Provide, Handle, effects or
// transactional scope entry. Advisory validation may read application state.
func (s *CommandScenario[C, R]) Validate(ctx context.Context, value C) (commands.Result[commands.NoResponse], error) {
	return s.scenario.application.Commands().Validate(ctx, value)
}

// QueryScenario performs a named registered snapshot query through the real query
// pipeline, including binding, authorization, validation, rendering, interception
// and operation-scope cleanup. It is not an observable/streaming scenario.
type QueryScenario[R any] struct {
	scenario *Scenario
	name     queries.FullyQualifiedQueryName
}

// NewQuery selects a fully qualified registered query name and expected data R.
// It borrows a non-nil Scenario made by New. Unknown names fail on Perform.
func NewQuery[R any](scenario *Scenario, name queries.FullyQualifiedQueryName) *QueryScenario[R] {
	return &QueryScenario[R]{scenario: scenario, name: name}
}

// Perform returns the actual result and inspectable error after scope cleanup.
// Use queries.RequestFor for typed arguments or a reader-created Request to test
// argument conversion without HTTP. ctx is forwarded unchanged to the pipeline.
func (s *QueryScenario[R]) Perform(ctx context.Context, request queries.Request) (queries.Result[R], error) {
	return queries.Perform[R](ctx, s.scenario.application.Queries(), s.name, request)
}
