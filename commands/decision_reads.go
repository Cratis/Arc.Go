// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"reflect"

	boundary "github.com/cratis/arc.go/internal/pipeline"
)

// This is a staged internal mechanism, NOT an enabled command profile. Pipeline
// admission, provider certification and dependency/Provide guards must be wired
// together before exposing it. In particular, a caller-supplied boolean cannot
// certify a provider or validator. No public API currently activates this code.
var errDecisionRead = errors.New("arc: decision read refused")

type decisionAdmission struct {
	protected, unprotected bool
	provider, owner        bool // facts supplied by the future trusted integration boundary
}

type decisionMode uint8

const (
	decisionProtected decisionMode = iota + 1
	decisionValidation
)

// The provider identity distinguishes clients even when store names match. These
// identities and callbacks will require a qualified cross-module seam; they are
// deliberately not exported or accepted from command payloads.
type decisionProviderIdentity struct{ _ byte }
type decisionTarget struct {
	provider              *decisionProviderIdentity
	model                 reflect.Type
	store, namespace, key string
}
type decisionSource struct {
	admit   func(context.Context) error
	acquire func(context.Context) (any, error)
	check   func(context.Context, any) error
	enroll  func(context.Context, any) error
}
type decisionRead struct {
	value any // opaque provider-issued read, never an Arc-manufactured token
	check func(context.Context, any) error
}
type decisionPending struct {
	done chan struct{}
	read *decisionRead
	err  error
}
type decisionReads struct {
	mode   decisionMode
	reads  map[decisionTarget]*decisionPending
	issued map[*decisionRead]struct{}
}

var decisionState = NewStateKey[*decisionReads]()

// checkDecisionAdmission refuses the initial narrow profile before any provider,
// validator or business callback. Arbitrary validators (including model methods
// and registered graph rules) cannot be certified: this checkpoint admits only
// WithoutModelValidation and no custom/scoped validators. Operations are refused.
func checkDecisionAdmission(r Registration, validationOnly bool, admission decisionAdmission) error {
	if !admission.protected || admission.unprotected || !admission.provider ||
		(!validationOnly && !admission.owner) || r.operations ||
		!r.withoutModel || len(r.validators) != 0 {
		return errDecisionRead
	}
	return nil
}

// beginDecisionReads attaches the already-admitted profile to a live frame. The
// pure check above must also run before OpenResources or other user factories.
func beginDecisionReads(ctx context.Context, inv *Invocation, admission decisionAdmission) error {
	return withState(ctx, inv, func(e *Execution) error {
		if err := checkDecisionAdmission(e.frame.registration, e.frame.snapshot.validationOnly, admission); err != nil {
			return err
		}
		if e.frame.state == nil {
			e.frame.state = make(map[*stateIdentity]any)
		}
		if _, exists := e.frame.state[decisionState.identity]; exists {
			return errDecisionRead // never reset provenance partway through a command
		}
		mode := decisionProtected
		if e.frame.snapshot.validationOnly {
			mode = decisionValidation
		}
		e.frame.state[decisionState.identity] = stateValue[*decisionReads]{&decisionReads{
			mode: mode, reads: make(map[decisionTarget]*decisionPending), issued: make(map[*decisionRead]struct{}),
		}}
		return nil
	})
}

func currentDecisionReads(e *Execution) (*decisionReads, error) {
	value, ok := e.frame.state[decisionState.identity].(stateValue[*decisionReads])
	if !ok || value.value == nil {
		return nil, errDecisionRead
	}
	return value.value, nil
}

// readDecision shares one in-flight acquisition per invocation/mode/target. The
// first acquisition's context owns the fold; canceled waiters do not cancel it.
// Acquisitions (including failures) are cached; enrollment is repeated on every
// resolution against the current owner. No callback runs under the state lock.
func readDecision(ctx context.Context, inv *Invocation, target decisionTarget, source decisionSource) (read *decisionRead, err error) {
	defer func() { err = decisionFailure(err) }()
	if target.provider == nil || target.model == nil || target.store == "" || target.namespace == "" || target.key == "" ||
		source.admit == nil || source.acquire == nil || source.check == nil || source.enroll == nil {
		return nil, errDecisionRead
	}
	var pending *decisionPending
	var state *decisionReads
	var first bool
	err = withState(ctx, inv, func(e *Execution) error {
		var err error
		state, err = currentDecisionReads(e)
		if err != nil {
			return err
		}
		pending = state.reads[target]
		if pending == nil {
			pending = &decisionPending{done: make(chan struct{})}
			state.reads[target], first = pending, true
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
			if err := source.admit(ctx); err != nil {
				return err
			}
			if err := inv.Execution().Check(ctx); err != nil {
				return err
			}
			value, err := source.acquire(ctx)
			if err != nil {
				return err
			}
			if nilValue(value) {
				return errDecisionRead
			}
			if err := inv.Execution().Check(ctx); err != nil {
				return err
			}
			if err := source.check(ctx, value); err != nil {
				return err
			}
			return withState(ctx, inv, func(e *Execution) error {
				current, err := currentDecisionReads(e)
				if err != nil || current != state {
					return errDecisionRead
				}
				pending.read = &decisionRead{value: value, check: source.check}
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
			return source.enroll(ctx, read.value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = withState(ctx, inv, func(e *Execution) error {
		current, err := currentDecisionReads(e)
		if err != nil || current != state {
			return errDecisionRead
		}
		current.issued[read] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return read, nil
}

// verifyDecision must precede dependency delivery and use of Provide's result.
// Checking only a provider token's type or LastHandled does not prove provenance.
func verifyDecision(ctx context.Context, inv *Invocation, read *decisionRead) (err error) {
	defer func() { err = decisionFailure(err) }()
	verify := func(e *Execution) error {
		state, err := currentDecisionReads(e)
		if err != nil || read == nil {
			return errDecisionRead
		}
		if _, issued := state.issued[read]; !issued {
			return errDecisionRead
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

// Acquisition/provenance failures are not advisory validation findings. Retain
// the original cause for errors.Is/As, but always add an infrastructure branch
// so an allowed validation severity cannot turn a refused read into success.
func decisionFailure(err error) error {
	if err != nil {
		return errors.Join(errDecisionRead, err)
	}
	return err
}
