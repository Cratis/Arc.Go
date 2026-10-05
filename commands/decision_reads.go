// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"reflect"

	boundary "github.com/cratis/arc.go/internal/pipeline"
)

// ErrDecisionRead identifies a refused decision read: an inadmissible profile,
// an uncertified provider, a failed provider stage, or a read that was not issued
// to the current invocation. The original cause is retained for errors.Is/As.
// It is always an infrastructure failure, never an advisory validation finding,
// so no allowed validation severity can turn a refused read into success.
var ErrDecisionRead = errors.New("arc: decision read refused")

// DecisionTarget identifies one read. Every field takes part in the invocation
// cache key, so the same model and key from another provider, store or namespace
// is a separate read. All fields are required.
type DecisionTarget struct {
	// Provider must be certified with Registry.AddDecisionProvider.
	Provider *DecisionProvider
	// Model is the read-model type.
	Model reflect.Type
	// Store, Namespace and Key select the instance within the provider.
	Store, Namespace, Key string
}

// DecisionSource is the provider-neutral seam a decision-read provider supplies
// for one target. Arc calls the stages in order, re-checking callback and
// security continuity between them, and never holds a lock while calling them.
// A stage that panics is converted to an error.
//
// Admit refuses unsupported models (for example classified ones) before any
// acquisition. Acquire returns the provider's opaque read evidence; it must not
// be nil and must not be manufactured by the application. Check re-validates that
// evidence (lifetime, target) on every issue and verification. Enroll registers
// the evidence with the provider's current completion owner so a competing write
// rejects the whole batch; Arc calls it for every protected issue, including
// cached ones, and never in validation-only execution.
type DecisionSource interface {
	Admit(context.Context) error
	Acquire(context.Context) (any, error)
	Check(context.Context, any) error
	Enroll(context.Context, any) error
}

// DecisionEvidence is implemented by values that carry issued decision reads. A
// protected command's Provide payload that implements it is verified before
// Handle runs; *DecisionRead implements it. Arc does not inspect other payload
// shapes, so a provider-typed decision value should implement this interface.
type DecisionEvidence interface{ DecisionReads() []*DecisionRead }

// DecisionRead is an issued read. Only ReadDecision issues one; a zero or copied
// value is never accepted. It is borrowed, invocation-owned evidence: verify it
// with VerifyDecision before relying on it in another callback.
type DecisionRead struct {
	value any // opaque provider-issued read, never an Arc-manufactured token
	check func(context.Context, any) error
}

// Value returns the provider's opaque evidence. It is not a proof by itself.
func (r *DecisionRead) Value() any {
	if r == nil {
		return nil
	}
	return r.value
}

// DecisionReads implements DecisionEvidence. A nil read yields one nil entry,
// which verification refuses.
func (r *DecisionRead) DecisionReads() []*DecisionRead { return []*DecisionRead{r} }

type decisionMode uint8

const (
	decisionProtected decisionMode = iota + 1
	decisionValidation
)

type decisionTarget struct {
	provider              *DecisionProvider
	model                 reflect.Type
	store, namespace, key string
}
type decisionPending struct {
	done chan struct{}
	read *DecisionRead
	err  error
}
type decisionReads struct {
	mode   decisionMode
	reads  map[decisionTarget]*decisionPending
	issued map[*DecisionRead]struct{}
}

var decisionState = NewStateKey[*decisionReads]()

// decisionsFor admits the frame and the target's provider, then returns the
// frame-local cache, creating it on first admitted use. The state lives in the
// frame, so nested commands and validation-only runs never share provenance, and
// it is discarded when the frame ends. Called under the state locks only.
func decisionsFor(e *Execution, provider *DecisionProvider) (*decisionReads, error) {
	f := e.frame
	if f.registration.decisions != DecisionsProtected {
		return nil, ErrDecisionProfile
	}
	if err := checkDecisionRegistration(f.registration); err != nil {
		return nil, err
	}
	if f.pipeline == nil {
		return nil, ErrDecisionProfile
	}
	if _, certified := f.pipeline.decisionProviders[provider]; !certified {
		return nil, ErrDecisionProfile
	}
	if !f.snapshot.validationOnly && len(f.pipeline.terminal) == 0 {
		return nil, ErrDecisionProfile // no completion owner can enroll the read
	}
	if value, ok := f.state[decisionState.identity].(stateValue[*decisionReads]); ok && value.value != nil {
		return value.value, nil
	}
	if f.state == nil {
		f.state = make(map[*stateIdentity]any)
	}
	mode := decisionProtected
	if f.snapshot.validationOnly {
		mode = decisionValidation
	}
	state := &decisionReads{mode: mode, reads: make(map[decisionTarget]*decisionPending), issued: make(map[*DecisionRead]struct{})}
	f.state[decisionState.identity] = stateValue[*decisionReads]{state}
	return state, nil
}

func currentDecisionReads(e *Execution) (*decisionReads, error) {
	value, ok := e.frame.state[decisionState.identity].(stateValue[*decisionReads])
	if !ok || value.value == nil {
		return nil, ErrDecisionRead
	}
	return value.value, nil
}

// ReadDecision issues a decision read for target within a protected command's
// invocation. It refuses, before any provider stage, an unmarked or unprotected
// command, an uncertified provider or a missing completion owner.
//
// One acquisition is shared per command frame and target, including concurrent
// callers; the first caller's context owns it and canceled waiters do not cancel
// it. Results, including failures, are cached for the frame and never retried.
// Every call re-checks the evidence and, outside validation-only execution,
// re-enrolls it with source's current owner. Validation-only execution uses a
// separate cache and never enrolls. Every failure wraps ErrDecisionRead.
func ReadDecision(ctx context.Context, inv *Invocation, target DecisionTarget, source DecisionSource) (read *DecisionRead, err error) {
	defer func() { err = decisionFailure(err) }()
	if target.Provider == nil || target.Model == nil || target.Store == "" || target.Namespace == "" || target.Key == "" || nilValue(source) {
		return nil, ErrInvalidRegistration
	}
	key := decisionTarget{provider: target.Provider, model: target.Model, store: target.Store, namespace: target.Namespace, key: target.Key}
	var pending *decisionPending
	var state *decisionReads
	var first bool
	err = withState(ctx, inv, func(e *Execution) error {
		var err error
		state, err = decisionsFor(e, target.Provider)
		if err != nil {
			return err
		}
		pending = state.reads[key]
		if pending == nil {
			pending = &decisionPending{done: make(chan struct{})}
			state.reads[key], first = pending, true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if first {
		// Call converts a panic to an error too, so all waiters are released and
		// a failed/panicking acquisition never leaves a permanently pending entry.
		pending.err = boundary.Call(ctx, func(ctx context.Context) error {
			if err := inv.Execution().Check(ctx); err != nil {
				return err
			}
			if err := source.Admit(ctx); err != nil {
				return err
			}
			if err := inv.Execution().Check(ctx); err != nil {
				return err
			}
			value, err := source.Acquire(ctx)
			if err != nil {
				return err
			}
			if nilValue(value) {
				return ErrDecisionRead
			}
			if err := inv.Execution().Check(ctx); err != nil {
				return err
			}
			if err := source.Check(ctx, value); err != nil {
				return err
			}
			return withState(ctx, inv, func(e *Execution) error {
				current, err := currentDecisionReads(e)
				if err != nil || current != state {
					return ErrDecisionRead
				}
				pending.read = &DecisionRead{value: value, check: source.Check}
				return nil
			})
		})
		close(pending.done)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pending.done:
	}
	if pending.err != nil {
		return nil, pending.err
	}
	if err := inv.Execution().Check(ctx); err != nil {
		return nil, err
	}
	read = pending.read
	err = boundary.Call(ctx, func(ctx context.Context) error {
		if err := read.check(ctx, read.value); err != nil {
			return err
		}
		// Provider checks may outlive the callback or change security continuity.
		// Refuse the next effect even when checking itself reports success.
		if err := inv.Execution().Check(ctx); err != nil {
			return err
		}
		if state.mode == decisionProtected {
			return source.Enroll(ctx, read.value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = withState(ctx, inv, func(e *Execution) error {
		current, err := currentDecisionReads(e)
		if err != nil || current != state {
			return ErrDecisionRead
		}
		current.issued[read] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return read, nil
}

// VerifyDecision refuses a nil, zero, foreign or expired read: one not issued to
// this command frame, issued to another command or a validation-only run, or
// whose evidence the provider no longer accepts. It re-checks callback and
// security continuity before and after the provider check. Checking only a
// provider token's type or progress does not prove provenance. Every failure
// wraps ErrDecisionRead.
func VerifyDecision(ctx context.Context, inv *Invocation, read *DecisionRead) (err error) {
	defer func() { err = decisionFailure(err) }()
	verify := func(e *Execution) error {
		state, err := currentDecisionReads(e)
		if err != nil || read == nil {
			return ErrDecisionRead
		}
		if _, issued := state.issued[read]; !issued {
			return ErrDecisionRead
		}
		return nil
	}
	if err := withState(ctx, inv, verify); err != nil {
		return err
	}
	if err := boundary.Call(ctx, func(ctx context.Context) error { return read.check(ctx, read.value) }); err != nil {
		return err
	}
	return withState(ctx, inv, verify)
}

// verifyProvided verifies every read carried by a Provide payload before Handle,
// for every profile: an unmarked command has no issued reads, so any carried read
// is refused. Payloads that carry no DecisionEvidence are not inspected.
func (f *frame) verifyProvided(payload any) error {
	evidence, ok := payload.(DecisionEvidence)
	if !ok {
		return nil
	}
	return f.call(func(ctx context.Context, inv *Invocation) error {
		if nilValue(evidence) {
			return decisionFailure(ErrDecisionRead)
		}
		reads := evidence.DecisionReads()
		if len(reads) == 0 {
			return decisionFailure(ErrDecisionRead)
		}
		for _, read := range reads {
			if err := VerifyDecision(ctx, inv, read); err != nil {
				return err
			}
		}
		return nil
	})
}

// decisionFailure retains the original cause for errors.Is/As but always adds an
// infrastructure branch, so an allowed validation severity cannot turn a refused
// read into success.
func decisionFailure(err error) error {
	if err == nil || errors.Is(err, ErrDecisionRead) {
		return err
	}
	return errors.Join(ErrDecisionRead, err)
}
