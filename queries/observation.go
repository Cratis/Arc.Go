// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/tenancy"
)

var (
	// ErrObservableCapability identifies custom snapshot-only pipelines.
	ErrObservableCapability = errors.New("query pipeline does not support observations")
	// ErrObservationRunning rejects reuse or overlapping observation consumers.
	ErrObservationRunning = errors.New("query observation already consumed or closing")
	// ErrObservationCapacity bounds active and unjoined source operations.
	ErrObservationCapacity = errors.New("query observation capacity exhausted")
	// ErrObservationsStopping rejects observation admission during shutdown.
	ErrObservationsStopping = errors.New("query observations are stopping")
	// ErrEnumerableRequiresStreaming preserves the reference plain HTTP contract.
	ErrEnumerableRequiresStreaming = errors.New("AsyncEnumerable queries require WebSocket connection")
	// ErrCompletedWithoutResult identifies completion before an allowed snapshot.
	ErrCompletedWithoutResult = errors.New("Observable query completed before producing its first result.") //nolint:staticcheck // Approved C# observable HTTP diagnostic, including punctuation.
)

// WaitTimeoutError is an approved framework diagnostic, not a provider exception.
// HTTP endpoints may map it to 408. Timeout is the applied (bounded) wait budget.
type WaitTimeoutError struct{ Timeout time.Duration }

func (e *WaitTimeoutError) Error() string {
	return fmt.Sprintf("Timed out waiting %s seconds for the first observable query result.", formatSeconds(e.Timeout))
}

// Unwrap permits inspection as context.DeadlineExceeded.
func (*WaitTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// ObservablePipeline adds owned observations without growing the Pipeline contract.
// CloseObservations stops new observation admission and joins active and failed
// opening cleanup. A timed-out join must be continued with a later call.
type ObservablePipeline interface {
	Pipeline
	Open(context.Context, FullyQualifiedQueryName, Request) (*Observation, Result[any], error)
	CloseObservations(context.Context) error
}

// TransferMode selects hub collection delivery. Direct deliveries use Full.
type TransferMode uint8

const (
	// Legacy carries full data plus changes for identity-bearing collections.
	Legacy TransferMode = iota
	// Full carries complete results without changes.
	Full
	// Delta carries an initial full result followed by changes.
	Delta
)

// ObservationOptions configures synchronous delivery. Its zero value selects
// Legacy. Direct transports and Subscribe explicitly select Full.
type ObservationOptions struct {
	TransferMode TransferMode
	// MaxBaselineBytes bounds each delivered/candidate immutable collection snapshot.
	// Zero means 16 MiB. Full and identity-less transfers retain no baseline.
	MaxBaselineBytes int64
	// ReserveBaseline reserves candidate bytes against host-wide/connection limits.
	// It returns a nonnil idempotent release callback, retained until replacement or
	// Run joins. Nil uses the per-snapshot ceiling only. It runs synchronously, never
	// under framework locks; callbacks must not reenter Close or mutate results.
	ReserveBaseline func(int64) (func(), error)
	// SkipEnumerableNull matches direct C# enumerable transports: null items
	// are skipped without acknowledging delivery. Subject nulls remain emissions.
	// Hubs leave this false to preserve nullable enumerable items.
	SkipEnumerableNull bool
}

type observationSource struct {
	open    func(context.Context) (observationStream, error)
	current func(context.Context) (any, bool, error)
}
type observationStream struct {
	next  func(context.Context) (any, error)
	close func(context.Context) error
}

func adaptSource[T any](value any) (observationSource, error) {
	source, ok := value.(observable.Source[T])
	if !ok || nilValue(source) {
		return observationSource{}, ErrResponseType
	}
	s := observationSource{open: func(ctx context.Context) (observationStream, error) {
		stream, err := source.Open(ctx)
		if nilValue(stream) {
			return observationStream{}, errors.Join(err, ErrResponseType)
		}
		return observationStream{
			next:  func(ctx context.Context) (any, error) { return stream.Next(ctx) },
			close: stream.Close,
		}, err
	}}
	if current, ok := source.(observable.CurrentSource[T]); ok {
		s.current = func(ctx context.Context) (any, bool, error) { return current.Current(ctx) }
	}
	return s, nil
}

// Observation owns a source stream and its operation scope. Run is single-use
// and synchronous; callbacks acknowledge delivery by returning nil. Close cancels
// and joins Run before closing the stream and resources. Never call Close from a
// delivery callback. Timed-out Close remains registered for shutdown joining.
type Observation struct {
	pipeline                                *queryPipeline
	ctx                                     context.Context
	cancel                                  context.CancelFunc
	scope                                   *execution.Scope
	query                                   Registration
	prepared                                authorization.Prepared
	metadata                                QueryContext
	arguments                               frozenValue
	subscription                            frozenValue
	admission                               Result[any]
	source                                  observationSource
	stream                                  observationStream
	mu                                      sync.Mutex
	active                                  chan struct{}
	consumed, closing, closed, streamClosed bool
	closeGate                               chan struct{}
	closeErr                                error
	release                                 func()
	first                                   bool
}

func (p *queryPipeline) track(o *Observation) error {
	p.observationMu.Lock()
	defer p.observationMu.Unlock()
	if p.observationsStopping {
		return ErrObservationsStopping
	}
	if len(p.observations) >= p.options.MaxObservations {
		return ErrObservationCapacity
	}
	p.observations[o] = struct{}{}
	return nil
}
func (p *queryPipeline) forget(o *Observation) {
	p.observationMu.Lock()
	delete(p.observations, o)
	p.observationMu.Unlock()
}

// CloseObservations atomically stops observation admission, cancels every owned
// operation before joining, and retains timed-out cleanup for a subsequent call.
// No cleanup goroutine is detached to manufacture a successful shutdown.
func (p *queryPipeline) CloseObservations(ctx context.Context) error {
	if ctx == nil {
		return execution.ErrInvalidArgument
	}
	p.observationMu.Lock()
	p.observationsStopping = true
	list := make([]*Observation, 0, len(p.observations))
	for o := range p.observations {
		list = append(list, o)
	}
	p.observationMu.Unlock()
	for _, o := range list {
		o.cancel()
	}
	var err error
	for _, o := range list {
		err = errors.Join(err, o.Close(ctx))
	}
	return err
}

func (p *queryPipeline) observableResult(ctx context.Context, name FullyQualifiedQueryName, result Result[any], err error) Result[any] {
	if err != nil {
		result = Merge(result, FromError[any](result.Details().CorrelationID, err))
		if p.options.Logger != nil && ctx != nil && ctx.Err() == nil {
			p.options.Logger.ErrorContext(ctx, "observable query failed", "query", string(name), "error", err)
		}
	}
	result = finalize(result, p.options.ExposeExceptionDetails)
	// Only exact approved errors may bypass redaction. Joined cleanup/provider
	// errors cannot accidentally expose their text as a trusted timeout message.
	_, timeout := err.(*WaitTimeoutError)
	if timeout || err == ErrCompletedWithoutResult {
		d := result.Details()
		d.ExceptionMessages = []string{err.Error()}
		result = NewResult(d, serialization.Optional[any]{})
	}
	return result
}

// Open runs admission and invokes the source factory once. Successful admission
// metadata is not an emission. Denials have nil observations. Every opened stream,
// including one returned with an error, remains owned until cleanup joins.
func (p *queryPipeline) Open(ctx context.Context, name FullyQualifiedQueryName, request Request) (*Observation, Result[any], error) {
	return p.openObservation(ctx, name, request, true)
}

func (p *queryPipeline) openObservation(ctx context.Context, name FullyQualifiedQueryName, request Request, activate bool) (*Observation, Result[any], error) {
	result := NewResult[any](Details{Ready: true, Authorized: true}, serialization.Optional[any]{})
	failure := func(err error) (*Observation, Result[any], error) {
		return nil, p.observableResult(ctx, name, result, err), err
	}
	if ctx == nil {
		return failure(execution.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	q, ok := p.Lookup(name)
	if !ok {
		return failure(ErrUnknownQuery)
	}
	if q.toSource == nil {
		return failure(ErrUnsupportedObservable)
	}
	id := correlation.FromContext(ctx)
	if id.IsZero() {
		var err error
		id, err = concepts.NewUUID()
		if err != nil {
			return failure(err)
		}
		ctx = correlation.WithID(ctx, id)
	}
	ctx, receipt, err := boundary.Receipt(ctx, p.options.Clock)
	if err != nil {
		return failure(err)
	}
	result = NewResult(Details{CorrelationID: id, Ready: true, Authorized: true}, serialization.Optional[any]{})
	if findings := request.parameters.Paging.Validate(); len(findings) > 0 {
		result = Merge(result, WithValidationResults[any](id, findings...))
		return failure(nil)
	}
	var a any
	if err := boundary.Call(ctx, func(context.Context) error { var err error; a, err = q.bind(request); return err }); err != nil {
		return failure(err)
	}
	prepared, err := p.options.Authorization.Prepare(ctx, authorization.Target{Kind: authorization.Query, Identity: string(name)})
	if err != nil {
		return failure(err)
	}
	c := QueryContext{name: name, correlationID: id, receivedAt: receipt.Round(0).UTC(), arguments: request.arguments, parameters: request.parameters}
	c.principal, _ = identity.PrincipalFrom(ctx)
	c.tenant, _ = tenancy.TenantFrom(ctx)
	if p.options.RequireTenant {
		if err := tenancy.Require(c.tenant); err != nil {
			result = Merge(result, Unauthorized[any](id, "tenant required"))
			return failure(err)
		}
	}
	work, cancel := context.WithCancel(ctx)
	o := &Observation{pipeline: p, ctx: work, cancel: cancel, query: q, prepared: prepared, metadata: c, active: make(chan struct{}), closeGate: make(chan struct{}, 1), first: true}
	if err := p.track(o); err != nil {
		cancel()
		return failure(err)
	}
	o.release = boundary.TakeObservationAdmission(work)
	subscription := &subscriptionScope{}
	work = context.WithValue(work, queryContextKey{}, c)
	work = context.WithValue(work, subscriptionScopeKey{}, subscription)
	o.ctx = work
	err = func() error {
		var err error
		o.scope, err = execution.OpenScope(work, p.options.OpenResources)
		if err != nil {
			var pending *execution.PendingScopeError
			if errors.As(err, &pending) {
				// Failed resource opening still transfers its cleanup ownership.
				o.scope = pending.Scope()
			}
			return err
		}
		return o.scope.Use(work, func(ctx context.Context, view *execution.Scope) error {
			result, err = p.admitQuery(ctx, view, prepared, q, a, c, result)
			if err != nil || !verdictSuccess(result) {
				return err
			}
			subscription.mu.Lock()
			subscription.frozen = true
			o.subscription = subscription.value
			subscription.mu.Unlock()
			if err := boundary.Call(ctx, func(context.Context) error { var err error; o.arguments, err = freezeValue(a); return err }); err != nil {
				return err
			}
			if !activate {
				return nil
			}
			value, err := p.invokeQuery(ctx, view, q, a, c)
			if err != nil {
				return err
			}
			return boundary.Call(ctx, func(ctx context.Context) error {
				var err error
				o.source, err = q.toSource(value)
				if err != nil {
					return err
				}
				o.stream, err = o.source.open(ctx)
				return err
			})
		})
	}()
	o.mu.Lock()
	close(o.active)
	o.active = nil
	o.mu.Unlock()
	if err != nil || !verdictSuccess(result) {
		err = errors.Join(err, o.cleanup())
		return failure(err)
	}
	o.admission = result
	return o, result, nil
}

func (o *Observation) cleanup() error {
	timeout := o.pipeline.options.ObservationCleanupTimeout
	if timeout == 0 {
		timeout = o.pipeline.options.CleanupTimeout
	}
	ctx, cancel, err := boundary.CleanupContext(o.ctx, timeout)
	if err != nil {
		return err
	}
	defer cancel()
	return o.Close(ctx)
}

// Close cancels observation work and synchronously joins it. A later call can
// continue after a timeout or unknown cleanup completion (ErrJoinPending).
// Recovered close panics retain ownership and diagnostics, never certify a join.
// The pipeline retains failed-opening cleanup until
// this completes; operation resources outlive source workers, never vice versa.
func (o *Observation) Close(ctx context.Context) error {
	if o == nil || ctx == nil {
		return execution.ErrInvalidArgument
	}
	o.cancel()
	o.mu.Lock()
	o.closing = true
	active := o.active
	o.mu.Unlock()
	if active != nil {
		select {
		case <-active:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case o.closeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-o.closeGate }()
	if o.closed {
		return o.closeErr
	}
	if !o.streamClosed && o.stream.close != nil {
		returned := false
		err := boundary.Call(ctx, func(ctx context.Context) error {
			err := o.stream.close(ctx)
			returned = true
			return err
		})
		if !returned {
			var diagnostic *execution.PanicError
			if errors.As(err, &diagnostic) {
				o.closeErr = errors.Join(o.closeErr, diagnostic)
			}
			return errors.Join(observable.ErrJoinPending, o.closeErr, err)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, observable.ErrJoinPending) {
			return errors.Join(o.closeErr, err)
		}
		o.closeErr = errors.Join(o.closeErr, err)
		o.streamClosed = true
	}
	if o.scope != nil {
		err := o.scope.Close(ctx)
		if errors.Is(err, execution.ErrScopeJoinPending) {
			return errors.Join(o.closeErr, err)
		}
		o.closeErr = errors.Join(o.closeErr, err)
	}
	o.closed = true
	o.pipeline.forget(o)
	if o.release != nil {
		o.release()
		o.release = nil
	}
	return o.closeErr
}

func (o *Observation) begin(ctx context.Context) error {
	if ctx == nil {
		return execution.ErrInvalidArgument
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.consumed || o.closing {
		return ErrObservationRunning
	}
	o.consumed = true
	o.active = make(chan struct{})
	return nil
}
func (o *Observation) end() {
	o.mu.Lock()
	close(o.active)
	o.active = nil
	o.mu.Unlock()
}

func (o *Observation) candidate(ctx context.Context, value any) (Result[any], EmissionVerdict, error) {
	result := o.admission
	verdict := Allow
	err := o.scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		arguments, err := o.arguments.thaw()
		if err != nil {
			return err
		}
		if o.pipeline.options.Membership != nil {
			var allowed bool
			err := boundary.Call(ctx, func(ctx context.Context) error {
				var err error
				allowed, err = o.pipeline.options.Membership.Authorize(ctx, o.metadata.principal, o.metadata.tenant)
				return err
			})
			if err != nil || !allowed {
				verdict = DenyAndTerminate
				return err
			}
		}
		decision, err := o.prepared.EvaluateScoped(ctx, view, arguments)
		if err != nil || !decision.IsAllowed() {
			verdict = DenyAndTerminate
			return err
		}
		var detached any
		if err := boundary.Call(ctx, func(context.Context) error { var err error; detached, err = detachValue(value); return err }); err != nil {
			return err
		}
		result, err = o.pipeline.renderEmission(ctx, view, o.prepared, o.query, detached, o.metadata, result)
		if err != nil {
			return err
		}
		for _, entry := range o.pipeline.guards {
			var guard EmissionGuard
			err := boundary.Call(ctx, func(ctx context.Context) error { var err error; guard, err = entry.factory(ctx, view); return err })
			if err != nil || nilValue(guard) {
				verdict = DenyAndTerminate
				return errors.Join(err, ErrInvalidRegistration)
			}
			var next EmissionVerdict
			err = boundary.Call(ctx, func(ctx context.Context) error {
				a, err := o.arguments.thaw()
				if err != nil {
					return err
				}
				s, err := o.subscription.thaw()
				if err != nil {
					return err
				}
				next, err = guard.Guard(ctx, EmissionContext{query: o.metadata, arguments: a, scope: s, first: o.first})
				return err
			})
			if err != nil || next > DenyAndTerminate {
				verdict = DenyAndTerminate
				return errors.Join(err, ErrInvalidRegistration)
			}
			if next == DenyAndTerminate {
				verdict = next
				return nil
			}
			if next == Suppress {
				verdict = Suppress
			}
		}
		if err := o.prepared.Check(ctx); err != nil {
			return err
		}
		if value, present := result.Data(); present {
			if err := boundary.Call(ctx, func(context.Context) error {
				detached, err := detachValue(value)
				if err == nil {
					result = NewResult(result.Details(), serialization.Some(detached))
				}
				return err
			}); err != nil {
				return err
			}
		}
		return view.CheckContext(ctx)
	})
	if verdict == DenyAndTerminate {
		result = Unauthorized[any](o.metadata.correlationID, "emission denied")
	}
	return o.pipeline.observableResult(ctx, o.metadata.name, result, err), verdict, err
}

// Run consumes serial emissions and waits for each delivery acknowledgement.
// io.EOF completes normally; source failure sends one safe terminal result.
// A denied candidate sends one unauthorized result and terminates. Suppression
// never satisfies a wait or advances the first-successful-delivery flag.
func (o *Observation) Run(ctx context.Context, options ObservationOptions, deliver func(Result[any]) error) error {
	if o == nil || deliver == nil {
		return execution.ErrInvalidArgument
	}
	if options.TransferMode > Delta || options.MaxBaselineBytes < 0 {
		return execution.ErrInvalidArgument
	}
	if options.MaxBaselineBytes == 0 {
		options.MaxBaselineBytes = 16 << 20
	}
	if err := o.begin(ctx); err != nil {
		return err
	}
	defer o.end()
	if err := o.scope.CheckContext(ctx); err != nil {
		return err
	}
	work, cancel := context.WithCancel(o.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	transfer := collectionTransfer{shape: o.query.collection, mode: options.TransferMode, limit: options.MaxBaselineBytes, reserve: options.ReserveBaseline}
	defer transfer.close()
	for {
		var value any
		err := boundary.Call(work, func(ctx context.Context) error { var err error; value, err = o.stream.next(ctx); return err })
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if work.Err() != nil {
				return work.Err()
			}
			result := o.pipeline.observableResult(work, o.metadata.name, o.admission, err)
			return errors.Join(err, boundary.Call(work, func(context.Context) error { return deliver(result) }))
		}
		if options.SkipEnumerableNull && o.query.enumerable && nilValue(value) {
			continue
		}
		result, verdict, err := o.candidate(work, value)
		if work.Err() != nil {
			return errors.Join(err, work.Err())
		}
		if verdict == Suppress && err == nil {
			continue
		}
		var hints collectionHints
		if observed, ok := value.(observedCollection); ok {
			hints = observed.collectionHints()
		}
		var commit, discard func()
		transferErr := boundary.Call(work, func(context.Context) error {
			var prepareErr error
			result, commit, discard, prepareErr = transfer.prepare(result, hints)
			return prepareErr
		})
		if transferErr != nil {
			// Preparation may have succeeded before the callback boundary observed
			// cancellation. Its candidate is not the delivered baseline and must
			// be discarded on every boundary failure.
			if discard != nil {
				discard()
			}
			result = o.pipeline.observableResult(work, o.metadata.name, o.admission, transferErr)
			return errors.Join(err, transferErr, boundary.Call(work, func(context.Context) error { return deliver(result) }))
		}
		deliveryErr := boundary.Call(work, func(context.Context) error { return deliver(result) })
		if deliveryErr != nil {
			discard()
			return errors.Join(err, deliveryErr)
		}
		commit()
		if verdict == DenyAndTerminate || err != nil {
			return err
		}
		o.first = false
	}
}

// Subscribe owns open/run/close and defaults to complete results. Compatibility
// is checked before activating the source. Custom snapshot-only pipelines fail.
func Subscribe[R any](ctx context.Context, p Pipeline, name FullyQualifiedQueryName, request Request, deliver func(Result[R]) error) error {
	if nilValue(p) || deliver == nil {
		return execution.ErrInvalidArgument
	}
	capability, ok := p.(ObservablePipeline)
	if !ok {
		return ErrObservableCapability
	}
	q, ok := p.Lookup(name)
	if !ok {
		return ErrUnknownQuery
	}
	if q.DataType() == nil || !q.DataType().AssignableTo(reflect.TypeFor[R]()) {
		return ErrResponseType
	}
	o, admission, err := capability.Open(ctx, name, request)
	if o == nil {
		return errors.Join(err, deliver(NewResult(admission.Details(), serialization.Optional[R]{})))
	}
	err = o.Run(ctx, ObservationOptions{TransferMode: Full}, func(result Result[any]) error {
		var data serialization.Optional[R]
		if value, present := result.Data(); present {
			if value == nil {
				data = serialization.Some(*new(R))
			} else {
				typed, ok := value.(R)
				if !ok {
					return ErrResponseType
				}
				data = serialization.Some(typed)
			}
		}
		return deliver(NewResult(result.Details(), data))
	})
	return errors.Join(err, o.cleanup())
}
