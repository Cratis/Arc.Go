// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/cratis/arc.go/execution"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// ErrInvalidOperation identifies an unsupported declaration or execution boundary.
var ErrInvalidOperation = errors.New("invalid command operation")

// Operation marks immutable business data describing immediate inline work.
// Payloads remain borrowed, including pointer declarations; do not mutate them
// during execution. Services belong in Execute/Compensate parameters, not payloads.
type Operation interface{ CommandOperation() }

// ExecutableOperation declares one typed, synchronous, error-only business call.
// Dependencies must be usable concurrently across commands. Arc never retries it.
type ExecutableOperation[D any] interface {
	Operation
	Execute(context.Context, D) error
}

// CompensatingOperation optionally reverses attempted work. D must contain every
// execution AND compensation dependency; registration preflights the whole bundle.
// Providers own partial-write, lost-acknowledgment, isolation and idempotency safety.
type CompensatingOperation[D any] interface {
	Compensate(context.Context, D, OperationFailure) error
}

// Operations is an ordered batch with copied membership and borrowed payloads.
// Zero is empty. It is safe for concurrent reads if payloads are not mutated.
type Operations struct{ values []Operation }

// NewOperations copies membership once and rejects nil members, including typed nils.
func NewOperations(values ...Operation) (Operations, error) {
	for _, value := range values {
		if nilValue(value) {
			return Operations{}, ErrInvalidOperation
		}
	}
	return Operations{values: append([]Operation(nil), values...)}, nil
}

// Values returns isolated ordered membership; declaration payloads remain borrowed.
func (o Operations) Values() []Operation { return append([]Operation(nil), o.values...) }

// WithOperations declares participation when Outcome effects erase operation types.
// Raw any returns are unsupported. Even empty/absent outputs require a compatible
// boundary. Validate never prepares, executes or compensates operations.
func WithOperations[C any]() Option[C] {
	return option[C]{"operations", func(c *configuration[C]) error { c.operations = true; return nil }}
}

// OperationOptions configures one cooperative, detached budget shared by recovery.
type OperationOptions struct {
	// CompensationTimeout defaults to 30 seconds when zero; negative is invalid.
	// A callback ignoring cancellation is still synchronously joined. After expiry
	// no further compensator starts; Arc never restarts a timed-out callback.
	CompensationTimeout time.Duration
}

type operationAdapter struct {
	keys    []di.Key
	prepare func(context.Context, *execution.Scope, Operation) (operationCall, error)
}
type operationCall struct {
	typ        reflect.Type
	execute    func(context.Context) error
	compensate func(context.Context, OperationFailure) error
}

// RegisterOperation registers exact O without activating services. Pointer/value
// types are distinct. dependencies resolves the COMPLETE execution/compensation
// bundle before any consumer or operation runs. Nil bundles are invalid. Empty
// struct bundles support no-dependency operations; a factory is still required.
// Optional DI keys are copied and checked against the existing catalog at Build.
// Business methods are invoked through typed calls, never reflection. A present
// but malformed Compensate method is a registration error, not an absent reversal.
func RegisterOperation[O ExecutableOperation[D], D any](r *Registry, dependencies Factory[D], keys ...di.Key) error {
	if r == nil || dependencies == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	typ := reflect.TypeFor[O]()
	if typ.Kind() == reflect.Interface {
		return ErrInvalidOperation
	}
	if _, exists := r.operations[typ]; exists {
		return ErrDuplicate
	}
	compensates := typ.Implements(reflect.TypeFor[CompensatingOperation[D]]())
	if _, present := typ.MethodByName("Compensate"); present && !compensates {
		return ErrInvalidOperation
	}
	// Do not silently omit a malformed or pointer-only compensator on value O.
	if typ.Kind() != reflect.Pointer {
		if _, present := reflect.PointerTo(typ).MethodByName("Compensate"); present && !compensates {
			return ErrInvalidOperation
		}
	}
	if r.operations == nil {
		r.operations = make(map[reflect.Type]operationAdapter)
	}
	r.operations[typ] = operationAdapter{keys: append([]di.Key(nil), keys...), prepare: func(ctx context.Context, scope *execution.Scope, value Operation) (operationCall, error) {
		dependency, err := dependencies(ctx, scope)
		if err != nil {
			return operationCall{}, err
		}
		if nilValue(dependency) {
			return operationCall{}, ErrInvalidOperation
		}
		operation := value.(O)
		call := operationCall{typ: typ, execute: func(ctx context.Context) error { return operation.Execute(ctx, dependency) }}
		if compensates {
			call.compensate = func(ctx context.Context, failure OperationFailure) error {
				return any(operation).(CompensatingOperation[D]).Compensate(ctx, dependency, failure)
			}
		}
		return call, nil
	}}
	return nil
}

func operationReturn(typ reflect.Type) bool {
	return typ != nil && (typ == reflect.TypeFor[Operations]() || typ.Implements(reflect.TypeFor[Operation]()))
}
func bareOperationCollection(typ reflect.Type) bool {
	return typ != nil && (typ.Kind() == reflect.Array || typ.Kind() == reflect.Slice) && operationReturn(typ.Elem())
}

// checkOperationResponseGraph preserves the explicit response reservation through
// the existing sealed graph grammar. It never searches DTOs or arbitrary containers.
func checkOperationResponseGraph(value any, depth int) error {
	if operationReturn(reflect.TypeOf(value)) || bareOperationCollection(reflect.TypeOf(value)) {
		return ErrInvalidOperation
	}
	graph, ok := value.(interface{ outcomeLeaves() []outcomeLeaf })
	if !ok || nilValue(value) {
		return nil
	}
	if depth >= 64 {
		return ErrUnhandledEffect
	}
	for _, leaf := range graph.outcomeLeaves() {
		if err := checkOperationResponseGraph(leaf.value, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// OperationFailureSource identifies the first failed pipeline phase.
type OperationFailureSource uint8

const (
	// FailurePlanning is declaration or dependency preflight failure.
	FailurePlanning OperationFailureSource = iota
	// FailureResponseHandling is return classification/control/consumer failure.
	FailureResponseHandling
	// FailureExecution is a business invocation failure.
	FailureExecution
	// FailureCancellation is cancellation of forward execution.
	FailureCancellation
	// FailureScopeCompletion is ordinary or terminal completion failure.
	FailureScopeCompletion
)

// OperationFailure supplies original failure facts to a compensator. Original is
// an immutable response-free snapshot; Completion is the sole persistence report.
// Cause preserves the original error identity, never later compensation errors.
type OperationFailure struct {
	InvocationIndex                          int
	InvocationCompleted, IsFailingInvocation bool
	Source                                   OperationFailureSource
	Completion                               CompletionReport
	Original                                 Result[NoResponse]
	Cause                                    error
}
