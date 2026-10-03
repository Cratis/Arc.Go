// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// PipelineOptions configures borrowed collaborators. OpenResources and ScopeFactory
// are mutually exclusive. Nil authorization compiles declarations with an empty
// policy registry, never bypassing security. CleanupTimeout zero means 30 seconds.
// Resources must open cheaply/lazily; direct shared extensions must be concurrent-safe.
type PipelineOptions struct {
	OpenResources     execution.OpenResources
	ScopeFactory      di.ScopeFactory
	DependencyCatalog di.Catalog
	Authorization     *authorization.Evaluator
	Validation        *validation.Graph
	Membership        tenancy.Membership
	RequireTenant     bool
	Clock             func() time.Time
	CleanupTimeout    time.Duration
	// MaximumWait bounds observable waits; zero means five minutes.
	MaximumWait time.Duration
	// MaxObservations includes active and unjoined observations; zero means 1024.
	MaxObservations int
	// ObservationCleanupTimeout overrides CleanupTimeout for stream joining.
	// Zero retains CleanupTimeout; root hosting supplies its five-second close grace.
	ObservationCleanupTimeout time.Duration
	ExposeExceptionDetails    bool
	Logger                    *slog.Logger
}

// Pipeline is the public snapshot substitution boundary. Perform opens independent
// resources; PerformScoped borrows compatible scope admission, never closing it.
// Implementations are concurrent-safe when application callbacks are.
type Pipeline interface {
	Lookup(FullyQualifiedQueryName) (Registration, bool)
	Perform(context.Context, FullyQualifiedQueryName, Request) (Result[any], error)
	PerformScoped(context.Context, *execution.Scope, FullyQualifiedQueryName, Request) (Result[any], error)
}
type queryPipeline struct {
	options              PipelineOptions
	queries              map[FullyQualifiedQueryName]Registration
	filters              []filterEntry
	interceptors         []interceptorEntry
	guards               []guardEntry
	observationMu        sync.Mutex
	observations         map[*Observation]struct{}
	observationsStopping bool
}

// Build freezes complete registrations and validates declarations, output ownership,
// renderer keys and dependency manifests without activation. Failure leaves r editable.
func (r *Registry) Build(o PipelineOptions) (Pipeline, error) {
	if r == nil {
		return nil, ErrInvalidRegistration
	}
	if r.frozen {
		return nil, ErrFrozen
	}
	if o.CleanupTimeout < 0 || o.MaximumWait < 0 || o.MaxObservations < 0 || o.ObservationCleanupTimeout < 0 || o.OpenResources != nil && o.ScopeFactory != nil {
		return nil, ErrInvalidRegistration
	}
	if o.ScopeFactory != nil && nilValue(o.ScopeFactory) || o.DependencyCatalog != nil && nilValue(o.DependencyCatalog) || o.Membership != nil && nilValue(o.Membership) {
		return nil, ErrInvalidRegistration
	}
	if o.MaximumWait == 0 {
		o.MaximumWait = 5 * time.Minute
	}
	if o.MaxObservations == 0 {
		o.MaxObservations = 1024
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.ScopeFactory != nil {
		o.OpenResources = execution.ResourcesFrom(o.ScopeFactory)
		if catalog, ok := o.ScopeFactory.(di.Catalog); ok && o.DependencyCatalog == nil {
			o.DependencyCatalog = catalog
		}
	}
	validateKeys := func(keys []di.Key) error {
		if len(keys) > 0 && o.DependencyCatalog == nil {
			return ErrInvalidRegistration
		}
		for _, key := range keys {
			if key.Type() == nil || !o.DependencyCatalog.Contains(key) {
				return &RegistrationError{Identity: key.String(), Kind: ErrInvalidRegistration}
			}
		}
		return nil
	}
	if err := o.Validation.CheckDependencies(o.DependencyCatalog); err != nil {
		return nil, err
	}
	p := &queryPipeline{options: o, queries: map[FullyQualifiedQueryName]Registration{}, filters: slices.Clone(r.filters), interceptors: slices.Clone(r.interceptors), guards: slices.Clone(r.guards), observations: map[*Observation]struct{}{}}
	catalog := r.Catalog()
	if _, err := metadata.Resolve(catalog, metadata.DefaultOptions()); err != nil {
		return nil, err
	}
	for _, q := range r.registrations {
		q = r.materialize(q)
		if q.renderer == nil && !q.page {
			if entry, ok := r.renderers[q.emissionType]; ok {
				copy := entry
				q.renderer = &copy
				q.dataType = entry.dataType
			}
		}
		if !validModelShape(q.modelType, q.dataType) {
			return nil, &RegistrationError{q.descriptor.Identity(), ErrResponseType}
		}
		if err := serialization.ValidateType(q.dataType); err != nil {
			return nil, err
		}
		var err error
		q.collection, err = compileCollection(q.dataType, q.identity)
		if err != nil {
			return nil, &RegistrationError{q.descriptor.Identity(), err}
		}
		if err := validateKeys(q.dependencies); err != nil {
			return nil, err
		}
		for _, v := range q.validators {
			if err := validateKeys(v.keys); err != nil {
				return nil, err
			}
		}
		if q.renderer != nil {
			if err := validateKeys(q.renderer.keys); err != nil {
				return nil, err
			}
		}
		name := FullyQualifiedQueryName(q.descriptor.Identity())
		if _, exists := p.queries[name]; exists {
			return nil, ErrDuplicate
		}
		p.queries[name] = q
	}
	for _, entry := range r.renderers {
		if err := validateKeys(entry.keys); err != nil {
			return nil, err
		}
	}
	for _, entry := range p.filters {
		if err := validateKeys(entry.keys); err != nil {
			return nil, err
		}
	}
	for _, entry := range p.interceptors {
		if err := validateKeys(entry.keys); err != nil {
			return nil, err
		}
	}
	for _, entry := range p.guards {
		if err := validateKeys(entry.keys); err != nil {
			return nil, err
		}
	}
	if o.Authorization == nil {
		var registry authorization.Registry
		e, err := registry.Build(catalog, authorization.Options{})
		if err != nil {
			return nil, err
		}
		p.options.Authorization = e
	} else if err := o.Authorization.CheckCatalog(catalog); err != nil {
		return nil, err
	}
	if err := p.options.Authorization.CheckDependencies(catalog, o.DependencyCatalog); err != nil {
		return nil, err
	}
	r.frozen = true
	return p, nil
}
func (p *queryPipeline) Lookup(name FullyQualifiedQueryName) (Registration, bool) {
	q, ok := p.queries[name]
	q.descriptor = copyDescriptor(q.descriptor)
	return q, ok
}

// Perform checks typed result compatibility before invoking application callbacks.
// Absence remains valid; incompatible known data contracts fail before admission.
func Perform[R any](ctx context.Context, p Pipeline, name FullyQualifiedQueryName, request Request) (Result[R], error) {
	var id correlation.ID
	if ctx != nil {
		id = correlation.FromContext(ctx)
	}
	expose := false
	if concrete, ok := p.(*queryPipeline); ok && concrete != nil {
		expose = concrete.options.ExposeExceptionDetails
	}
	failure := func(err error) (Result[R], error) {
		return finalize(FromError[R](id, err), expose), err
	}
	if nilValue(p) {
		return failure(ErrInvalidRegistration)
	}
	q, ok := p.Lookup(name)
	if !ok {
		return failure(ErrUnknownQuery)
	}
	target := reflect.TypeFor[R]()
	if q.DataType() == nil || !q.DataType().AssignableTo(target) {
		return failure(ErrResponseType)
	}
	result, err := p.Perform(ctx, name, request)
	d := result.Details()
	data, present := result.Data()
	if !present {
		return NewResult(d, serialization.Optional[R]{}), err
	}
	if data == nil {
		return NewResult(d, serialization.Some(*new(R))), err
	}
	value, ok := data.(R)
	if !ok {
		return finalize(Merge(NewResult(d, serialization.Optional[R]{}), FromError[any](d.CorrelationID, ErrResponseType)), expose), errors.Join(err, ErrResponseType)
	}
	return NewResult(d, serialization.Some(value)), err
}
func (p *queryPipeline) Perform(ctx context.Context, name FullyQualifiedQueryName, r Request) (Result[any], error) {
	if q, ok := p.Lookup(name); ok && q.toSource != nil {
		return p.observableSnapshot(ctx, name, r)
	}
	return p.perform(ctx, nil, name, r, false)
}
func (p *queryPipeline) PerformScoped(ctx context.Context, s *execution.Scope, name FullyQualifiedQueryName, r Request) (Result[any], error) {
	if q, ok := p.Lookup(name); ok && q.toSource != nil {
		return finalize(FromError[any](correlation.FromContext(ctx), ErrUnsupportedObservable), p.options.ExposeExceptionDetails), ErrUnsupportedObservable
	}
	return p.perform(ctx, s, name, r, true)
}
func (p *queryPipeline) perform(ctx context.Context, scope *execution.Scope, name FullyQualifiedQueryName, request Request, borrowed bool) (Result[any], error) {
	var id correlation.ID
	result := NewResult[any](Details{Ready: true, Authorized: true}, serialization.Optional[any]{})
	finish := func(err error) (Result[any], error) {
		if err != nil {
			fragment := FromError[any](id, err)
			if p.options.ExposeExceptionDetails {
				d := fragment.Details()
				failure := boundary.Classify(err)
				d.ExceptionMessages = nil
				for _, exception := range failure.Exceptions {
					d.ExceptionMessages = append(d.ExceptionMessages, exception.Error())
				}
				var panicErr *execution.PanicError
				if errors.As(err, &panicErr) {
					d.ExceptionStackTrace = string(panicErr.Stack)
				}
				fragment = NewResult(d, serialization.Optional[any]{})
			}
			result = Merge(result, fragment)
			if p.options.Logger != nil {
				p.options.Logger.ErrorContext(ctx, "query failed", "query", string(name), "error", err)
			}
		}
		return finalize(result, p.options.ExposeExceptionDetails), err
	}
	if ctx == nil {
		return finish(execution.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return finish(err)
	}
	q, ok := p.Lookup(name)
	if !ok {
		return finish(ErrUnknownQuery)
	}
	// Establish receipt without changing principal/tenant presence or borrowed authority.
	id = correlation.FromContext(ctx)
	if id.IsZero() {
		var err error
		id, err = concepts.NewUUID()
		if err != nil {
			return finish(err)
		}
		ctx = correlation.WithID(ctx, id)
	}
	ctx, receipt, receiptErr := boundary.Receipt(ctx, p.options.Clock)
	if receiptErr != nil {
		return finish(receiptErr)
	}
	d := result.Details()
	d.CorrelationID = id
	result = NewResult(d, serialization.Optional[any]{})
	if findings := request.parameters.Paging.Validate(); len(findings) > 0 {
		result = Merge(result, WithValidationResults[any](id, findings...))
		return finish(nil)
	}
	var arguments any
	if err := boundary.Call(ctx, func(context.Context) error { var err error; arguments, err = q.bind(request); return err }); err != nil {
		return finish(err)
	}
	prepared, err := p.options.Authorization.Prepare(ctx, authorization.Target{Kind: authorization.Query, Identity: string(name)})
	if err != nil {
		return finish(err)
	}
	principal, _ := identity.PrincipalFrom(ctx)
	tenant, _ := tenancy.TenantFrom(ctx)
	if p.options.RequireTenant {
		if err := tenancy.Require(tenant); err != nil {
			result = Merge(result, Unauthorized[any](id, "tenant required"))
			return finish(err)
		}
	}
	if borrowed {
		if scope == nil {
			return finish(execution.ErrInvalidScope)
		}
		if p.options.ScopeFactory != nil {
			owner, ok := p.options.ScopeFactory.(di.ScopeOwner)
			if !ok {
				return finish(execution.ErrScopeOwner)
			}
			if err := scope.CheckOwner(ctx, owner); err != nil {
				return finish(err)
			}
		}
	} else {
		scope, err = execution.OpenScope(ctx, p.options.OpenResources)
		if err != nil {
			return finish(err)
		}
	}
	c := QueryContext{name: name, correlationID: id, receivedAt: receipt.Round(0).UTC(), principal: principal, tenant: tenant, arguments: request.arguments, parameters: request.parameters}
	ctx = context.WithValue(ctx, queryContextKey{}, c)
	err = scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		var err error
		result, err = p.core(ctx, view, prepared, q, arguments, c, result)
		return err
	})
	if !borrowed {
		cleanup, cancel, cleanupErr := boundary.CleanupContext(ctx, p.options.CleanupTimeout)
		if cleanupErr == nil {
			cleanupErr = scope.Close(cleanup)
			cancel()
		}
		err = errors.Join(err, cleanupErr)
	}
	return finish(err)
}
func (p *queryPipeline) core(ctx context.Context, s *execution.Scope, prepared authorization.Prepared, q Registration, a any, c QueryContext, result Result[any]) (Result[any], error) {
	result, err := p.admitQuery(ctx, s, prepared, q, a, c, result)
	if err != nil || !verdictSuccess(result) {
		return result, err
	}
	data, err := p.invokeQuery(ctx, s, q, a, c)
	if err != nil {
		return result, err
	}
	return p.renderEmission(ctx, s, prepared, q, data, c, result)
}

// admitQuery runs input filters and validation once, without activating a source.
func (p *queryPipeline) admitQuery(ctx context.Context, s *execution.Scope, prepared authorization.Prepared, q Registration, a any, c QueryContext, result Result[any]) (Result[any], error) {
	if p.options.Membership != nil {
		var allowed bool
		err := boundary.Call(ctx, func(ctx context.Context) error {
			var err error
			allowed, err = p.options.Membership.Authorize(ctx, c.principal, c.tenant)
			return err
		})
		if err != nil || !allowed {
			return Merge(result, Unauthorized[any](c.correlationID, "tenant membership required")), err
		}
	}
	decision, err := prepared.EvaluateScoped(ctx, s, a)
	if err != nil || !decision.IsAllowed() {
		return Merge(result, Unauthorized[any](c.correlationID, decision.Reason())), err
	}
	for _, auth := range []bool{true, false} {
		for _, entry := range p.filters {
			if entry.authorization != auth {
				continue
			}
			var filter Filter
			err := s.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
				return boundary.Call(ctx, func(ctx context.Context) error { var err error; filter, err = entry.factory(ctx, view); return err })
			})
			if err != nil {
				return result, err
			}
			if nilValue(filter) {
				return result, ErrInvalidRegistration
			}
			var fragment Result[any]
			err = s.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
				return boundary.Call(ctx, func(ctx context.Context) error {
					var err error
					fragment, err = filter.OnPerform(ctx, &Invocation{queryContext: c, scope: view, subscriptionScope: subscriptionScopeFrom(ctx)})
					return err
				})
			})
			if _, present := fragment.Data(); present || fragment.Details().ChangeSet != nil {
				return result, errors.Join(err, ErrResponseType)
			}
			for _, finding := range fragment.Details().ValidationResults {
				if finding.Severity < validation.Unknown || finding.Severity > validation.Error {
					return result, errors.Join(err, &validation.InvocationError{Cause: validation.ErrInvalidSeverity})
				}
			}
			result = Merge(result, fragment)
			if err != nil || !verdictSuccess(result) {
				return result, err
			}
		}
	}
	if !q.withoutModel {
		findings, err := p.options.Validation.Validate(ctx, s, a)
		result = Merge(result, WithValidationResults[any](c.correlationID, findings...))
		if err != nil || !verdictSuccess(result) {
			return result, err
		}
	}
	for _, validator := range q.validators {
		var findings []validation.Result
		err := s.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
			return boundary.Call(ctx, func(ctx context.Context) error {
				var err error
				findings, err = validator.invoke(ctx, view, a)
				return err
			})
		})
		result = Merge(result, WithValidationResults[any](c.correlationID, findings...))
		if err != nil || !verdictSuccess(result) {
			return result, err
		}
	}
	if err := prepared.Check(ctx); err != nil {
		return result, err
	}
	// Reevaluate declarations immediately before invocation; verdicts are never cached.
	decision, err = prepared.EvaluateScoped(ctx, s, a)
	if err != nil || !decision.IsAllowed() {
		return Merge(result, Unauthorized[any](c.correlationID, decision.Reason())), err
	}
	return result, nil
}

func (p *queryPipeline) invokeQuery(ctx context.Context, s *execution.Scope, q Registration, a any, c QueryContext) (any, error) {
	var data any
	err := s.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		return boundary.Call(ctx, func(ctx context.Context) error {
			var err error
			data, err = q.invoke(ctx, &Invocation{queryContext: c, scope: view}, a)
			return err
		})
	})
	return data, err
}

// renderEmission is shared by unary and observable deliveries. Admission and
// invocation remain separate so input filters are never rerun per emission.
func (p *queryPipeline) renderEmission(ctx context.Context, s *execution.Scope, prepared authorization.Prepared, q Registration, data any, c QueryContext, result Result[any]) (Result[any], error) {
	var err error
	var total int64
	if nilValue(data) {
		// A nil provider output is ready-null, not a renderer invocation.
		data = nil
	} else if q.page {
		data, total = data.(pageValue).pageData()
	} else if q.renderer != nil {
		err = s.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
			var err error
			data, total, err = q.renderer.invoke(ctx, view, data, c)
			return err
		})
		if err != nil {
			return result, err
		}
	} else if observed, ok := data.(observedCollection); ok {
		data = observed.collectionItems()
	}
	if q.toSource != nil {
		if err := boundary.Call(ctx, func(context.Context) error { var err error; data, err = detachValue(data); return err }); err != nil {
			return result, err
		}
	}
	c.totalItems = total
	ctx = context.WithValue(ctx, queryContextKey{}, c)
	if !nilValue(data) {
		err = s.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
			var err error
			data, err = intercept(ctx, view, data, q.dataType, p.interceptors)
			return err
		})
		if err != nil {
			return result, err
		}
	}
	if err := prepared.Check(ctx); err != nil {
		return result, err
	}
	if err := s.CheckContext(ctx); err != nil {
		return result, err
	}
	d := result.Details()
	if (q.page || q.renderer != nil) && c.parameters.Paging.IsPaged {
		d.Paging = PagingInfo{Page: int32(c.parameters.Paging.Page), Size: int32(c.parameters.Paging.Size), TotalItems: total}
	}
	return NewResult(d, serialization.Some(data)), nil
}
func verdictSuccess(r Result[any]) bool { return r.IsAuthorized() && r.IsValid() && !r.HasExceptions() }

type parameterFailure struct{ findings []validation.Result }

func (*parameterFailure) Error() string                            { return "invalid query parameters" }
func (e *parameterFailure) ValidationResults() []validation.Result { return e.findings }
