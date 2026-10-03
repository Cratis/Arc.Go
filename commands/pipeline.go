// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// PipelineOptions configures borrowed shared collaborators. Nil authorization
// compiles declarations (never bypasses them); nil validation enables model/tag rules.
// OpenResources and ScopeFactory are mutually exclusive. Cleanup is cooperative.
type PipelineOptions struct {
	OpenResources          execution.OpenResources
	ScopeFactory           di.ScopeFactory
	DependencyCatalog      di.Catalog
	Authorization          *authorization.Evaluator
	Validation             *validation.Graph
	Membership             tenancy.Membership
	RequireTenant          bool
	Clock                  func() time.Time
	CleanupTimeout         time.Duration
	Operations             OperationOptions
	ExposeExceptionDetails bool
	Logger                 *slog.Logger
}

// ExecuteOptions supplies zero or one caller allowance; command floors still apply.
type ExecuteOptions struct{ AllowedSeverity *validation.Severity }

// Pipeline is the public substitution boundary for backend command execution.
type Pipeline interface {
	Lookup(string) (Registration, bool)
	LookupCommand(any) (Registration, error)
	Execute(context.Context, any, ...ExecuteOptions) (Result[any], error)
	Validate(context.Context, any, ...ExecuteOptions) (Result[NoResponse], error)
	ExecuteScoped(context.Context, *execution.Scope, any, ...ExecuteOptions) (Result[any], error)
	ValidateScoped(context.Context, *execution.Scope, any, ...ExecuteOptions) (Result[NoResponse], error)
}
type pipeline struct {
	options      PipelineOptions
	byType       map[reflect.Type]Registration
	byName       map[string]Registration
	providers    []extension[ContextValuesProvider]
	keys         []extension[KeyResolver]
	filters      []extension[Filter]
	authFilters  []extension[AuthorizationFilter]
	responses    []extension[ResponseValueHandler]
	participants []extension[ExecutionScope]
	terminal     []extension[DeferredCommitParticipant]
	admissions   []ReturnAdmission
	models       map[reflect.Type]readModelProvider
	operations   map[reflect.Type]operationAdapter
}

func (p *pipeline) Lookup(name string) (Registration, bool) { r, ok := p.byName[name]; return r, ok }
func (p *pipeline) LookupCommand(command any) (Registration, error) {
	if nilValue(command) {
		return Registration{}, ErrInvalidRegistration
	}
	r, ok := p.byType[reflect.TypeOf(command)]
	if !ok {
		return Registration{}, ErrMissingHandler
	}
	return r, nil
}
func (p *pipeline) Execute(ctx context.Context, command any, options ...ExecuteOptions) (Result[any], error) {
	return p.run(ctx, nil, command, false, nil, options)
}
func (p *pipeline) Validate(ctx context.Context, command any, options ...ExecuteOptions) (Result[NoResponse], error) {
	result, err := p.run(ctx, nil, command, true, nil, options)
	return NewResult(result.Details(), serialization.Optional[NoResponse]{}), err
}
func (p *pipeline) ExecuteScoped(ctx context.Context, scope *execution.Scope, command any, options ...ExecuteOptions) (Result[any], error) {
	if bound := operationBoundaryFor(ctx, p); bound != nil {
		child, _ := p.LookupCommand(command)
		if bound.frame.registration.operations || child.operations {
			return bound.run(ctx, scope, command, false, options)
		}
	}
	if scope == nil {
		return FromError[any](contextID(ctx), execution.ErrInvalidScope), execution.ErrInvalidScope
	}
	return p.run(ctx, scope, command, false, nil, options)
}
func (p *pipeline) ValidateScoped(ctx context.Context, scope *execution.Scope, command any, options ...ExecuteOptions) (Result[NoResponse], error) {
	if bound := operationBoundaryFor(ctx, p); bound != nil {
		child, _ := p.LookupCommand(command)
		if bound.frame.registration.operations || child.operations {
			result, err := bound.run(ctx, scope, command, true, options)
			return NewResult(result.Details(), serialization.Optional[NoResponse]{}), err
		}
	}
	if scope == nil {
		return FromError[NoResponse](contextID(ctx), execution.ErrInvalidScope), execution.ErrInvalidScope
	}
	result, err := p.run(ctx, scope, command, true, nil, options)
	return NewResult(result.Details(), serialization.Optional[NoResponse]{}), err
}

// Execute checks a known response contract before application code. Unknown
// contracts are checked against the actual response after execution; a mismatch
// returns ErrResponseType and a failed envelope with no response.
func Execute[R any](ctx context.Context, p Pipeline, command any, options ...ExecuteOptions) (Result[R], error) {
	if nilValue(p) {
		return FromError[R](contextID(ctx), ErrInvalidRegistration), ErrInvalidRegistration
	}
	if bound := operationBoundaryFor(ctx, p); bound != nil {
		child, _ := bound.pipeline.LookupCommand(command)
		if bound.frame.registration.operations || child.operations {
			result, err := bound.run(ctx, bound.scope, command, false, options)
			return NewResult(result.Details(), serialization.Optional[R]{}), err
		}
	}
	registration, err := p.LookupCommand(command)
	if err == nil {
		t := reflect.TypeFor[R]()
		if registration.responseKind == ResponseValue && !registration.responseType.AssignableTo(t) {
			err = ErrResponseType
		}
	}
	if err != nil {
		if bound, ok := p.(*boundPipeline); ok {
			bound.record(FromError[any](contextID(ctx), err), err)
		}
		return FromError[R](contextID(ctx), err), err
	}
	result, err := p.Execute(ctx, command, options...)
	var response serialization.Optional[R]
	if value, ok := result.Response(); ok {
		typed, ok := value.(R)
		if !ok {
			err = errors.Join(err, ErrResponseType)
			return Merge(NewResult(result.Details(), response), FromError[NoResponse](result.Details().CorrelationID, ErrResponseType)), withCompletionError(err, result.Completion())
		}
		response = serialization.Some(typed)
	}
	return NewResult(result.Details(), response), err
}
func contextID(ctx context.Context) correlation.ID {
	if ctx == nil {
		return correlation.ID{}
	}
	return correlation.FromContext(ctx)
}

type frame struct {
	pipeline        *pipeline
	registration    Registration
	ctx             context.Context
	scope           *execution.Scope
	snapshot        CommandContext
	prepared        authorization.Prepared
	policy          validation.Policy
	result          Result[any]
	err             error
	owner           *executionState
	parent          *frame
	parentContext   CommandContext
	operations      *operationJournal
	operationPhase  OperationFailureSource
	operationCommit OperationCommitParticipant
	state           map[*stateIdentity]any // guarded by owner.mu
	ended           bool                   // guarded by owner.mu
	// Nested fragments are guarded by owner.mu, independently for each frame.
	nested    Result[NoResponse]
	nestedErr error
	nestedSet bool
}

func (p *pipeline) run(ctx context.Context, borrowed *execution.Scope, command any, validationOnly bool, parent *frame, options []ExecuteOptions) (result Result[any], err error) {
	registration, err := p.LookupCommand(command)
	if parent == nil && ctx != nil {
		if bound, ok := ctx.Value(callbackBoundaryKey{}).(*boundPipeline); ok && bound.pipeline == p && (bound.frame.registration.operations || registration.operations) {
			return bound.run(ctx, bound.scope, command, validationOnly, options)
		}
	}
	if err == nil && (ctx == nil || len(options) > 1) {
		err = ErrInvalidRegistration
	}
	var allowed *validation.Severity
	if len(options) == 1 && options[0].AllowedSeverity != nil {
		value := *options[0].AllowedSeverity
		allowed = &value
	}
	policy, policyErr := validation.NewPolicy(validation.SeverityOptions{Allowed: allowed, BlockOn: registration.descriptor.BlockOnValidationSeverity})
	err = errors.Join(err, policyErr)
	if err != nil {
		return FromError[any](contextID(ctx), err), err
	}
	id, err := correlation.Resolve(ctx, "")
	if err != nil {
		return FromError[any](contextID(ctx), err), err
	}
	ctx = correlation.WithID(ctx, id)
	clock := p.options.Clock
	if clock == nil {
		clock = time.Now
	}
	ctx, received, err := boundary.Receipt(ctx, clock)
	if err != nil {
		return FromError[any](id, err), err
	}
	prepared, err := p.options.Authorization.Prepare(ctx, authorization.Target{Kind: authorization.Command, Identity: registration.descriptor.Type.Identity()})
	if err != nil {
		return FromError[any](id, err), err
	}
	principal, _ := identity.PrincipalFrom(ctx)
	tenant, _ := tenancy.TenantFrom(ctx)
	if p.options.RequireTenant {
		if err := tenancy.Require(tenant); err != nil {
			return Merge(FromError[any](id, err), Unauthorized(id, "tenant required")), err
		}
	}
	f := &frame{pipeline: p, registration: registration, ctx: ctx, prepared: prepared, policy: policy, parent: parent,
		snapshot: CommandContext{descriptor: registration.Descriptor(), command: command, correlation: id, received: received.Round(0).UTC(), principal: principal, tenant: tenant, values: ContextValues{entries: make(map[string]any)}, allowed: allowed, validationOnly: validationOnly}, result: NewResult(Details{CorrelationID: id, Authorized: true}, serialization.Optional[any]{})}
	if parent == nil {
		f.owner = &executionState{top: f}
	} else {
		f.owner = parent.owner
		f.parentContext = parent.snapshot
		if current, ok := ContextFrom(ctx); ok {
			f.parentContext = current
		}
		f.parentContext.values = f.parentContext.Values()
		f.owner.mu.Lock()
		f.owner.top = f
		f.owner.mu.Unlock()
	}
	defer func() {
		f.owner.mu.Lock()
		f.ended, f.state = true, nil
		if parent == nil {
			f.owner.closed, f.owner.values, f.owner.top = true, nil, nil
		} else {
			f.owner.top = parent
		}
		f.owner.mu.Unlock()
	}()
	scope := borrowed
	owned := scope == nil
	if owned {
		scope, err = execution.OpenScope(ctx, p.options.OpenResources)
	}
	if err != nil {
		f.fail(err, false)
		return f.final(), f.err
	}
	if !owned && p.options.ScopeFactory != nil {
		owner, ok := p.options.ScopeFactory.(di.ScopeOwner)
		if !ok {
			err = execution.ErrScopeOwner
		} else {
			err = scope.CheckOwner(ctx, owner)
		}
		if err != nil {
			f.fail(err, false)
			return f.final(), f.err
		}
	}
	err = scope.Use(ctx, func(_ context.Context, view *execution.Scope) error {
		f.scope = view
		f.execute()
		return nil
	})
	f.fail(err, false)
	f.mergeNested()
	if owned {
		cleanup, cancel, cleanupErr := boundary.CleanupContext(ctx, p.options.CleanupTimeout)
		if cleanupErr == nil {
			cleanupErr = scope.Close(cleanup)
			cancel()
		}
		f.fail(cleanupErr, false)
	}
	return f.final(), withCompletionError(f.err, f.completionReport())
}
func (f *frame) final() Result[any] {
	response := serialization.Optional[any]{}
	if f.snapshot.hasResponse {
		response = serialization.Some(f.snapshot.response)
	}
	details := f.result.Details()
	details.Completion = f.completionReport()
	if f.operations != nil {
		details.operations = &operationObservations{summary: f.operations.summary, outcomes: f.operations.outcomes}
	}
	return NewResult(details, response)
}
func (f *frame) merge(fragment Result[NoResponse], filter bool) {
	d := fragment.Details()
	for _, finding := range d.ValidationResults {
		if finding.Severity < validation.Unknown || finding.Severity > validation.Error {
			f.fail(validation.ErrInvalidSeverity, false)
			return
		}
	}
	if filter {
		d.ValidationResults = f.policy.Filter(d.ValidationResults)
	}
	if !f.pipeline.options.ExposeExceptionDetails && len(d.ExceptionMessages) != 0 {
		for i := range d.ExceptionMessages {
			d.ExceptionMessages[i] = boundary.InternalErrorMessage
		}
		d.ExceptionStackTrace = ""
	}
	f.result = Merge(f.result, NewResult(d, serialization.Optional[NoResponse]{}))
	f.captureOperationFailure()
}
func (f *frame) fail(err error, filter bool) {
	if err == nil {
		return
	}
	failure := boundary.Classify(err)
	if filter && len(failure.Findings) > 0 && len(failure.Exceptions) == 0 && len(f.policy.Filter(failure.Findings)) == 0 {
		// Nonblocking findings are not a failure, even when delivered as an error.
		return
	}
	f.err = errors.Join(f.err, err)
	fragment := FromError[NoResponse](f.snapshot.correlation, err)
	if f.pipeline.options.ExposeExceptionDetails {
		d := fragment.Details()
		d.ExceptionMessages = nil
		for _, exception := range boundary.Classify(err).Exceptions {
			if errors.Is(exception, authorization.ErrDenied) {
				continue
			}
			d.ExceptionMessages = append(d.ExceptionMessages, exception.Error())
			var panicErr *execution.PanicError
			if errors.As(exception, &panicErr) {
				d.ExceptionStackTrace += string(panicErr.Stack)
			}
		}
		fragment = NewResult(d, serialization.Optional[NoResponse]{})
	}
	f.merge(fragment, filter)
	if f.result.IsSuccess() {
		// An unclassified/empty failure must never produce success with an error.
		f.fail(ErrInvalidPreparation, false)
	}
	if logger := f.pipeline.options.Logger; logger != nil {
		logger.ErrorContext(f.ctx, "Command callback failed", "command", f.registration.descriptor.Type.Identity(), "correlationId", f.snapshot.correlation, "error", err)
	}
}
func (f *frame) call(call func(context.Context, *Invocation) error) error {
	return f.callWith(f.ctx, call)
}
func (f *frame) callWith(ctx context.Context, call func(context.Context, *Invocation) error) error {
	return f.scope.Use(ctx, func(ctx context.Context, view *execution.Scope) (err error) {
		snapshot := f.snapshot
		snapshot.values = snapshot.Values()
		inv := &Invocation{active: true, snapshot: snapshot, scope: view}
		inv.owner = &Execution{state: f.owner, frame: f, invocation: inv}
		inv.bound = &boundPipeline{pipeline: f.pipeline, frame: f, invocation: inv, scope: view}
		f.owner.mu.Lock()
		attemptsBefore := f.owner.operationAttempts
		f.owner.mu.Unlock()
		defer func() {
			err = errors.Join(err, inv.bound.expire())
			f.owner.mu.Lock()
			newAttempts := f.owner.operationAttempts != attemptsBefore
			f.owner.mu.Unlock()
			if newAttempts {
				err = errors.Join(err, ErrInvalidOperation)
			}
			inv.mu.Lock()
			inv.active = false
			f.snapshot.values = inv.snapshot.Values()
			inv.mu.Unlock()
		}()
		ctx = context.WithValue(ctx, commandContextKey{}, snapshot)
		ctx = context.WithValue(ctx, callbackBoundaryKey{}, inv.bound)
		return boundary.Call(ctx, func(ctx context.Context) error { return call(ctx, inv) })
	})
}
func activate[T any](f *frame, entry extension[T]) (value T, err error) {
	err = f.call(func(ctx context.Context, inv *Invocation) error {
		var err error
		value, err = entry.factory(ctx, inv.Scope())
		return err
	})
	if err == nil && nilValue(value) {
		err = ErrInvalidRegistration
	}
	return value, err
}
func (f *frame) execute() {
	if f.registration.operations && !operationScopesCompatible(f.pipeline.participants, f.pipeline.terminal) {
		f.fail(ErrInvalidOperation, false)
		return
	}
	f.contextValues()
	if !f.result.IsSuccess() {
		return
	}
	var entered []ExecutionScope
	var terminal DeferredCommitParticipant
	beginParticipants := func() {
		if f.snapshot.validationOnly || f.parent != nil {
			return
		}
		if len(f.pipeline.terminal) != 0 {
			candidate, err := activate(f, f.pipeline.terminal[0])
			f.fail(err, false)
			if err == nil {
				terminal = candidate
				if f.registration.operations {
					f.operationCommit = candidate.(OperationCommitParticipant)
				}
				f.fail(f.call(terminal.Begin), false)
			}
		}
		for _, entry := range f.pipeline.participants {
			if !f.result.IsSuccess() {
				break
			}
			participant, err := activate(f, entry)
			f.fail(err, false)
			if err != nil {
				break
			}
			entered = append(entered, participant)
			f.fail(f.call(participant.Begin), false)
			if !f.result.IsSuccess() {
				break
			}
		}
	}
	if !f.registration.operations {
		beginParticipants()
	}
	defer func() {
		if f.parent != nil {
			return
		}
		f.mergeNested()
		if f.snapshot.validationOnly {
			return
		}
		// Completion observes cancellation before getting its live cleanup context.
		f.operationPhase = FailureScopeCompletion
		f.fail(f.ctx.Err(), false)
		f.captureOperationFailure()
		defer f.recoverOperations()
		cleanup, cancel, err := boundary.CleanupContext(f.ctx, f.pipeline.options.CleanupTimeout)
		if err != nil {
			f.fail(err, false)
			return
		}
		defer cancel()
		for i := len(entered) - 1; i >= 0; i-- {
			var fragment Result[NoResponse]
			err := f.callWith(cleanup, func(ctx context.Context, inv *Invocation) error {
				var err error
				fragment, err = entered[i].Complete(ctx, inv, f.final())
				return err
			})
			if err == nil {
				f.merge(fragment, false)
			}
			f.fail(err, false)
			f.mergeNested()
			f.captureOperationFailure()
		}
		if !nilValue(terminal) {
			f.fail(f.ctx.Err(), false)
			f.completeTerminal(cleanup, terminal)
			f.mergeNested()
			f.captureOperationFailure()
		}
	}()
	if !f.result.IsSuccess() {
		return
	}
	if f.pipeline.options.Membership != nil {
		allowed := false
		err := f.call(func(ctx context.Context, _ *Invocation) error {
			var err error
			allowed, err = f.pipeline.options.Membership.Authorize(ctx, f.snapshot.principal, f.snapshot.tenant)
			return err
		})
		if !allowed || err != nil {
			f.merge(Unauthorized(f.snapshot.correlation, "tenant membership required"), false)
		}
		f.fail(err, false)
		if !f.result.IsSuccess() {
			return
		}
	}
	var decision authorization.Decision
	err := f.call(func(ctx context.Context, inv *Invocation) error {
		var err error
		decision, err = f.prepared.EvaluateScoped(ctx, inv.Scope(), f.snapshot.command)
		return err
	})
	if !decision.IsAllowed() || err != nil {
		f.merge(Unauthorized(f.snapshot.correlation, decision.Reason()), false)
	}
	f.fail(err, false)
	if !f.result.IsSuccess() {
		return
	}
	for _, entry := range f.pipeline.authFilters {
		filter, err := activate(f, entry)
		f.fail(err, false)
		if err != nil {
			return
		}
		f.runFilter(filter)
		if !f.result.IsSuccess() {
			return
		}
	}
	for _, entry := range f.pipeline.filters {
		filter, err := activate(f, entry)
		f.fail(err, true)
		if err != nil || !f.result.IsSuccess() {
			return
		}
		f.runFilter(filter)
		if !f.result.IsSuccess() {
			return
		}
	}
	f.validate()
	if !f.result.IsSuccess() || f.snapshot.validationOnly {
		return
	}
	if f.registration.operations {
		beginParticipants()
		if !f.result.IsSuccess() {
			return
		}
	}
	var prepared preparedCall
	err = f.call(func(ctx context.Context, inv *Invocation) error {
		var err error
		prepared, err = f.registration.adapter.prepare(ctx, inv, f.snapshot.command)
		return err
	})
	f.fail(err, true)
	if err != nil {
		if f.result.IsSuccess() {
			f.fail(ErrInvalidPreparation, false)
		}
		return
	}
	if prepared.hasControl {
		f.merge(prepared.control, true)
	}
	if prepared.stop || !f.result.IsSuccess() {
		return
	}
	err = f.prepared.Check(f.ctx)
	if err == nil {
		err = f.scope.CheckContext(f.ctx)
	}
	f.fail(err, false)
	if err != nil {
		return
	}
	var output any
	err = f.call(func(ctx context.Context, inv *Invocation) error {
		var err error
		output, err = prepared.handle(ctx, inv)
		return err
	})
	f.fail(err, false)
	if err != nil {
		return
	}
	f.process(output)
}
func (f *frame) runFilter(filter Filter) {
	var fragment Result[NoResponse]
	err := f.call(func(ctx context.Context, inv *Invocation) error {
		var err error
		fragment, err = filter.OnExecution(ctx, inv)
		return err
	})
	if err == nil {
		f.merge(fragment, true)
	}
	f.fail(err, true)
}
func (f *frame) contextValues() {
	for _, entry := range f.pipeline.providers {
		provider, err := activate(f, entry)
		f.fail(err, false)
		if err != nil {
			return
		}
		var values ContextValues
		err = f.call(func(ctx context.Context, inv *Invocation) error {
			var err error
			values, err = provider.Provide(ctx, inv)
			return err
		})
		f.fail(err, false)
		if err != nil {
			return
		}
		for name, value := range values.entries {
			f.snapshot.values.entries[name] = value
		}
	}
	if _, exists := f.snapshot.values.Get(ResolvedKey); exists {
		return
	}
	for _, entry := range f.pipeline.keys {
		resolver, err := activate(f, entry)
		f.fail(err, false)
		if err != nil {
			return
		}
		var key string
		var exists bool
		err = f.call(func(ctx context.Context, inv *Invocation) error {
			var err error
			key, exists, err = resolver.Resolve(ctx, inv)
			return err
		})
		f.fail(err, false)
		if err != nil {
			return
		}
		if exists && key != "" {
			f.snapshot.values.entries["resolvedkey"] = key
			return
		}
	}
	var key string
	var exists bool
	err := f.call(func(context.Context, *Invocation) error {
		if provider, ok := f.snapshot.command.(KeyProvider); ok {
			key = provider.GetKey()
			exists = key != ""
		}
		if !exists && f.registration.key != nil {
			var err error
			key, exists, err = f.registration.key(f.snapshot.command)
			if err != nil {
				return err
			}
		}
		return nil
	})
	f.fail(err, false)
	if err == nil && exists && key != "" {
		f.snapshot.values.entries["resolvedkey"] = key
	}
}
