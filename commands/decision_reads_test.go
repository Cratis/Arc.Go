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

// These tests drive the frame mechanism directly. Provider objects here are test
// values, not Chronicle decision tokens. Pipeline-level behavior is specified in
// decision_pipeline_test.go.
var testDecisionProvider = NewDecisionProvider()

func decisionPipelineForTest() *pipeline {
	return &pipeline{
		decisionProviders: decisionProviderSet([]*DecisionProvider{testDecisionProvider}),
		terminal:          []extension[DeferredCommitParticipant]{{name: "owner"}},
	}
}

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
	f := &frame{ctx: t.Context(), scope: scope, pipeline: decisionPipelineForTest(),
		registration: Registration{withoutModel: true, decisions: DecisionsProtected}, snapshot: CommandContext{validationOnly: validation}}
	f.owner = &executionState{top: f}
	return f
}

type testDecisionSource struct {
	admit   func(context.Context) error
	acquire func(context.Context) (any, error)
	check   func(context.Context, any) error
	enroll  func(context.Context, any) error
}

func (s testDecisionSource) Admit(ctx context.Context) error            { return s.admit(ctx) }
func (s testDecisionSource) Acquire(ctx context.Context) (any, error)   { return s.acquire(ctx) }
func (s testDecisionSource) Check(ctx context.Context, value any) error { return s.check(ctx, value) }
func (s testDecisionSource) Enroll(ctx context.Context, value any) error {
	return s.enroll(ctx, value)
}

func decisionSourceForTest(acquired, enrolled *atomic.Int32) (DecisionTarget, testDecisionSource) {
	target := DecisionTarget{Provider: testDecisionProvider, Model: reflect.TypeFor[string](), Store: "store", Namespace: "tenant", Key: "key"}
	source := testDecisionSource{
		admit: func(context.Context) error { return nil },
		acquire: func(context.Context) (any, error) {
			acquired.Add(1)
			return new(int), nil
		},
		check: func(_ context.Context, value any) error {
			if _, ok := value.(*int); !ok {
				return ErrDecisionRead
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

func TestDecisionAdmissionRefusesBeforeAnyProviderStage(t *testing.T) {
	for _, name := range []string{"unmarked", "unprotected", "uncertified-provider", "no-pipeline", "no-owner", "model-validation", "validator", "operations", "invalid-target", "nil-source"} {
		t.Run(name, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			var src DecisionSource = source
			wantProfile := true
			switch name {
			case "unmarked":
				f.registration.decisions = DecisionsUnmarked
			case "unprotected":
				f.registration.decisions = DecisionsUnprotected
			case "uncertified-provider":
				target.Provider = NewDecisionProvider()
			case "no-pipeline":
				f.pipeline = nil
			case "no-owner":
				f.pipeline.terminal = nil
			case "model-validation":
				f.registration.withoutModel = false
			case "validator":
				f.registration.validators = []validatorEntry{{}}
			case "operations":
				f.registration.operations = true
			case "invalid-target":
				target.Key, wantProfile = "", false
			case "nil-source":
				src, wantProfile = (*testDecisionSource)(nil), false
			}
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				read, err := ReadDecision(ctx, inv, target, src)
				if read != nil || !errors.Is(err, ErrDecisionRead) || errors.Is(err, ErrDecisionProfile) != wantProfile {
					t.Fatalf("refused admission = %v, %v", read, err)
				}
				return nil
			})
			if acquired.Load() != 0 || enrolled.Load() != 0 {
				t.Fatal("refused admission reached provider")
			}
		})
	}
}

func TestDecisionValidationOnlyNeedsNoOwnerAndNeverEnrolls(t *testing.T) {
	f := decisionFrameForTest(t, true)
	f.pipeline.terminal = nil
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
		read, err := ReadDecision(ctx, inv, target, source)
		if err != nil {
			return err
		}
		return VerifyDecision(ctx, inv, read)
	})
	if acquired.Load() != 1 || enrolled.Load() != 0 {
		t.Fatal("validation-only read", acquired.Load(), enrolled.Load())
	}
}

func TestCurrentDecisionProfileReportsTheFrameProfile(t *testing.T) {
	for _, profile := range []DecisionProfile{DecisionsUnmarked, DecisionsProtected, DecisionsUnprotected} {
		f := decisionFrameForTest(t, false)
		f.registration.decisions = profile
		var retained *Invocation
		callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
			retained = inv
			got, err := CurrentDecisionProfile(ctx, inv)
			if err != nil || got != profile {
				t.Fatalf("profile = %v, %v; want %v", got, err, profile)
			}
			return nil
		})
		if _, err := CurrentDecisionProfile(t.Context(), retained); !errors.Is(err, ErrExecutionClosed) {
			t.Fatal("expired invocation reported a profile", err)
		}
	}
	if _, err := CurrentDecisionProfile(t.Context(), nil); !errors.Is(err, ErrNoContext) {
		t.Fatal("nil invocation", err)
	}
}

func TestDecisionCacheSurvivesCallbacksButNotFramesOrValidation(t *testing.T) {
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	var parentRead, validationRead *DecisionRead
	var expired *Invocation
	parent := decisionFrameForTest(t, false)
	callDecisionFrame(t, parent, func(ctx context.Context, inv *Invocation) error {
		expired = inv
		var err error
		parentRead, err = ReadDecision(ctx, inv, target, source)
		return err
	})
	callDecisionFrame(t, parent, func(ctx context.Context, inv *Invocation) error {
		if err := VerifyDecision(ctx, expired, parentRead); !errors.Is(err, ErrExecutionClosed) {
			t.Fatalf("retained callback = %v", err)
		}
		got, err := ReadDecision(ctx, inv, target, source)
		if err != nil || got != parentRead {
			t.Fatalf("second callback = %v, %v", got, err)
		}
		return VerifyDecision(ctx, inv, got)
	})
	for _, validating := range []bool{true, false} {
		child := decisionFrameForTest(t, validating)
		child.pipeline = parent.pipeline
		// A bound nested frame shares the owner, never the parent's cache.
		child.parent, child.owner = parent, parent.owner
		parent.owner.top = child
		callDecisionFrame(t, child, func(ctx context.Context, inv *Invocation) error {
			for _, foreign := range []*DecisionRead{nil, {}, parentRead, validationRead} {
				if err := VerifyDecision(ctx, inv, foreign); !errors.Is(err, ErrDecisionRead) {
					t.Fatal("foreign/null/zero read accepted", err)
				}
			}
			got, err := ReadDecision(ctx, inv, target, source)
			if err != nil || got == parentRead || got == validationRead {
				t.Fatalf("child read = %v, %v", got, err)
			}
			if validating {
				validationRead = got
			}
			return VerifyDecision(ctx, inv, got)
		})
		child.ended, child.state = true, nil
		parent.owner.top = parent
	}
	callDecisionFrame(t, parent, func(ctx context.Context, inv *Invocation) error { return VerifyDecision(ctx, inv, parentRead) })
	if acquired.Load() != 3 || enrolled.Load() != 3 {
		t.Fatalf("acquired = %d, enrolled = %d", acquired.Load(), enrolled.Load())
	}
}

func TestOverwrittenDecisionHandleIsRefusedRegardlessOfEvidenceShape(t *testing.T) {
	shared := new(int)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"same-pointer", shared},
		{"same-scalar", 1},
		{"slice", []int{1}},
		{"map", map[string]int{"one": 1}},
		{"struct-with-slice", struct{ Values []int }{[]int{1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			source.acquire = func(context.Context) (any, error) { return tc.value, nil }
			source.check = func(context.Context, any) error { return nil }
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				first, err := ReadDecision(ctx, inv, target, source)
				if err != nil {
					return err
				}
				target.Key = "other"
				second, err := ReadDecision(ctx, inv, target, source)
				if err != nil {
					return err
				}
				if err := VerifyDecision(ctx, inv, second); err != nil {
					return err
				}
				*first = *second
				if err := VerifyDecision(ctx, inv, first); !errors.Is(err, ErrDecisionRead) {
					t.Fatalf("overwritten handle verified: %v", err)
				}
				return nil
			})
		})
	}
}

func TestCachedDecisionReadRefusesOverwriteDuringProviderCallbacks(t *testing.T) {
	for _, stage := range []string{"check", "enroll"} {
		for _, replacement := range []string{"foreign", "zero"} {
			t.Run(stage+"/"+replacement, func(t *testing.T) {
				f := decisionFrameForTest(t, false)
				var acquired, enrolled atomic.Int32
				target, source := decisionSourceForTest(&acquired, &enrolled)
				var read *DecisionRead
				var overwrite DecisionRead
				var armed bool
				check := source.check
				source.check = func(ctx context.Context, value any) error {
					if armed && stage == "check" {
						*read = overwrite
					}
					return check(ctx, value)
				}
				callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
					var err error
					read, err = ReadDecision(ctx, inv, target, source)
					if err != nil {
						return err
					}
					if replacement == "foreign" {
						other := target
						other.Key = "other"
						foreign, err := ReadDecision(ctx, inv, other, source)
						if err != nil {
							return err
						}
						overwrite = *foreign
					}
					before := enrolled.Load()
					armed = true
					if stage == "enroll" {
						source.enroll = func(context.Context, any) error {
							enrolled.Add(1)
							*read = overwrite
							return nil
						}
					}
					got, err := ReadDecision(ctx, inv, target, source)
					if got != nil || !errors.Is(err, ErrDecisionRead) {
						t.Fatalf("overwritten during %s = %v, %v", stage, got, err)
					}
					want := before
					if stage == "enroll" {
						want++
					}
					if enrolled.Load() != want {
						t.Fatalf("enrolled = %d, want %d", enrolled.Load(), want)
					}
					return nil
				})
			})
		}
	}
}

func TestDecisionCacheSeparatesEveryTargetDimension(t *testing.T) {
	f := decisionFrameForTest(t, false)
	var acquired, enrolled atomic.Int32
	base, source := decisionSourceForTest(&acquired, &enrolled)
	callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
		for _, dimension := range []string{"base", "client", "model", "store", "namespace", "key"} {
			target := base
			switch dimension {
			case "client":
				target.Provider = NewDecisionProvider()
				f.pipeline.decisionProviders[target.Provider] = struct{}{}
			case "model":
				target.Model = reflect.TypeFor[int]()
			case "store":
				target.Store = "other"
			case "namespace":
				target.Namespace = "other"
			case "key":
				target.Key = "other"
			}
			if _, err := ReadDecision(ctx, inv, target, source); err != nil {
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
				for range 2 {
					if read, err := ReadDecision(ctx, inv, target, source); read != nil || err == nil {
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
		var wg sync.WaitGroup
		results := make(chan *DecisionRead, 8)
		for range 8 {
			wg.Go(func() {
				read, err := ReadDecision(ctx, inv, target, source)
				if err != nil {
					t.Error(err)
				}
				results <- read
			})
		}
		<-entered
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if got, err := ReadDecision(canceled, inv, target, source); got != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("canceled waiter = %v, %v", got, err)
		}
		close(release)
		wg.Wait()
		close(results)
		var first *DecisionRead
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
