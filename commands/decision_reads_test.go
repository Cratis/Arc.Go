// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cratis/arc.go/execution"
)

// These tests drive the private frame mechanism, not protected-command pipeline
// wiring. Provider objects here are test values, not Chronicle decision tokens.
func decisionFrameForTest(t *testing.T, validation bool) *frame {
	t.Helper()
	scope, err := execution.OpenScope(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	f := &frame{ctx: t.Context(), scope: scope, registration: Registration{withoutModel: true}, snapshot: CommandContext{validationOnly: validation}}
	f.owner = &executionState{top: f}
	return f
}

func decisionAdmissionForTest() decisionAdmission {
	return decisionAdmission{protected: true, provider: true, owner: true}
}
func decisionSourceForTest(acquired, enrolled *atomic.Int32) (decisionTarget, decisionSource) {
	target := decisionTarget{provider: &decisionProviderIdentity{}, model: reflect.TypeFor[string](), store: "store", namespace: "tenant", key: "key"}
	source := decisionSource{
		admit: func(context.Context) error { return nil },
		acquire: func(context.Context) (any, error) {
			acquired.Add(1)
			return new(int), nil
		},
		check: func(_ context.Context, value any) error {
			if _, ok := value.(*int); !ok {
				return errDecisionRead
			}
			return nil
		},
		enroll: func(context.Context, any) error { enrolled.Add(1); return nil },
	}
	return target, source
}
func callDecisionFrame(t *testing.T, f *frame, call func(context.Context, *Invocation) error) {
	t.Helper()
	if err := f.call(call); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionAdmissionRefusesBeforeProviderOrValidatorConstruction(t *testing.T) {
	for _, name := range []string{"legacy", "unprotected", "conflicting", "provider", "owner", "model-validation", "validator", "operations"} {
		t.Run(name, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			admission := decisionAdmissionForTest()
			switch name {
			case "legacy":
				admission.protected = false
			case "unprotected":
				admission.protected, admission.unprotected = false, true
			case "conflicting":
				admission.unprotected = true
			case "provider":
				admission.provider = false
			case "owner":
				admission.owner = false
			case "model-validation":
				f.registration.withoutModel = false
			case "validator":
				f.registration.validators = []validatorEntry{{}}
			case "operations":
				f.registration.operations = true
			}
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				if err := beginDecisionReads(ctx, inv, admission); !errors.Is(err, errDecisionRead) {
					t.Fatalf("admission = %v", err)
				}
				if read, err := readDecision(ctx, inv, target, source); read != nil || !errors.Is(err, errDecisionRead) {
					t.Fatalf("read after refused admission = %v, %v", read, err)
				}
				return nil
			})
			if acquired.Load() != 0 || enrolled.Load() != 0 {
				t.Fatal("refused admission reached provider")
			}
		})
	}
}

func TestDecisionCacheSurvivesCallbacksButNotFramesOrValidation(t *testing.T) {
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	var parentRead, validationRead *decisionRead
	var expired *Invocation
	parent := decisionFrameForTest(t, false)
	callDecisionFrame(t, parent, func(ctx context.Context, inv *Invocation) error {
		expired = inv
		if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
			return err
		}
		var err error
		parentRead, err = readDecision(ctx, inv, target, source)
		return err
	})
	callDecisionFrame(t, parent, func(ctx context.Context, inv *Invocation) error {
		if err := verifyDecision(ctx, expired, parentRead); !errors.Is(err, ErrExecutionClosed) {
			t.Fatalf("retained callback = %v", err)
		}
		got, err := readDecision(ctx, inv, target, source)
		if err != nil || got != parentRead {
			t.Fatalf("second callback = %v, %v", got, err)
		}
		if err := verifyDecision(ctx, inv, got); err != nil {
			return err
		}
		if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); !errors.Is(err, errDecisionRead) {
			t.Fatal("allowed provenance reset", err)
		}
		return nil
	})
	for _, validating := range []bool{true, false} {
		child := decisionFrameForTest(t, validating)
		// A bound nested frame shares the owner, never the parent's cache.
		child.parent, child.owner = parent, parent.owner
		parent.owner.top = child
		callDecisionFrame(t, child, func(ctx context.Context, inv *Invocation) error {
			admission := decisionAdmissionForTest()
			admission.owner = !validating
			if err := beginDecisionReads(ctx, inv, admission); err != nil {
				return err
			}
			for _, foreign := range []*decisionRead{nil, {}, parentRead, validationRead} {
				if err := verifyDecision(ctx, inv, foreign); !errors.Is(err, errDecisionRead) {
					t.Fatal("foreign/null/zero read accepted", err)
				}
			}
			got, err := readDecision(ctx, inv, target, source)
			if err != nil || got == parentRead || got == validationRead {
				t.Fatalf("child read = %v, %v", got, err)
			}
			if validating {
				validationRead = got
			}
			return verifyDecision(ctx, inv, got)
		})
		child.ended, child.state = true, nil
		parent.owner.top = parent
	}
	callDecisionFrame(t, parent, func(ctx context.Context, inv *Invocation) error { return verifyDecision(ctx, inv, parentRead) })
	if acquired.Load() != 3 || enrolled.Load() != 3 {
		t.Fatalf("acquired = %d, enrolled = %d", acquired.Load(), enrolled.Load())
	}
}

func TestDecisionCacheSeparatesEveryTargetDimension(t *testing.T) {
	f := decisionFrameForTest(t, false)
	var acquired, enrolled atomic.Int32
	base, source := decisionSourceForTest(&acquired, &enrolled)
	callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
		if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
			return err
		}
		for _, dimension := range []string{"base", "client", "model", "store", "namespace", "key"} {
			target := base
			switch dimension {
			case "client":
				target.provider = &decisionProviderIdentity{}
			case "model":
				target.model = reflect.TypeFor[int]()
			case "store":
				target.store = "other"
			case "namespace":
				target.namespace = "other"
			case "key":
				target.key = "other"
			}
			if _, err := readDecision(ctx, inv, target, source); err != nil {
				return err
			}
		}
		return nil
	})
	if acquired.Load() != 6 || enrolled.Load() != 6 {
		t.Fatal("targets were aliased", acquired.Load(), enrolled.Load())
	}
}

func TestDecisionFailuresNeverIssueAndAreNotRetried(t *testing.T) {
	failure := errors.New("provider refusal")
	for _, kind := range []string{"admission", "acquisition", "nil", "typed-nil", "zero", "panic", "enrollment", "stale"} {
		t.Run(kind, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			switch kind {
			case "admission":
				source.admit = func(context.Context) error { return failure }
			case "acquisition", "nil", "typed-nil", "zero", "panic":
				source.acquire = func(context.Context) (any, error) {
					acquired.Add(1)
					switch kind {
					case "acquisition":
						return nil, failure
					case "typed-nil":
						return (*int)(nil), nil
					case "zero":
						return struct{}{}, nil
					case "panic":
						panic("provider panic")
					}
					return nil, nil
				}
			case "enrollment":
				source.enroll = func(context.Context, any) error { return failure }
			case "stale":
				source.check = func(context.Context, any) error { return failure }
			}
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
					return err
				}
				for range 2 {
					if read, err := readDecision(ctx, inv, target, source); read != nil || err == nil {
						t.Fatalf("failed read = %v, %v", read, err)
					}
				}
				return nil
			})
			if acquired.Load() > 1 || enrolled.Load() != 0 {
				t.Fatal("failure retried acquisition or enrolled", acquired.Load(), enrolled.Load())
			}
		})
	}
}

func TestDecisionConcurrentReadsShareFoldAndCanceledWaiterDoesNotEnroll(t *testing.T) {
	f := decisionFrameForTest(t, false)
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	entered, release := make(chan struct{}), make(chan struct{})
	source.acquire = func(context.Context) (any, error) {
		acquired.Add(1)
		close(entered)
		<-release
		return new(int), nil
	}
	callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
		if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
			return err
		}
		var wg sync.WaitGroup
		results := make(chan *decisionRead, 8)
		for range 8 {
			wg.Go(func() {
				read, err := readDecision(ctx, inv, target, source)
				if err != nil {
					t.Error(err)
				}
				results <- read
			})
		}
		<-entered
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if got, err := readDecision(canceled, inv, target, source); got != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("canceled waiter = %v, %v", got, err)
		}
		close(release)
		wg.Wait()
		close(results)
		var first *decisionRead
		for read := range results {
			if first == nil {
				first = read
			}
			if read == nil || read != first {
				t.Error("concurrent resolutions differ")
			}
		}
		return nil
	})
	if acquired.Load() != 1 || enrolled.Load() != 8 {
		t.Fatal("fold/enrollment counts", acquired.Load(), enrolled.Load())
	}
}
