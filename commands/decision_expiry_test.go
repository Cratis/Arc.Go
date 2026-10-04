// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
)

func TestDecisionIssuedReadRechecksProviderAndCurrentInvocation(t *testing.T) {
	f := decisionFrameForTest(t, false)
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	stale := errors.New("stale provider evidence")
	var invalid atomic.Bool
	source.check = func(context.Context, any) error {
		if invalid.Load() {
			return stale
		}
		return nil
	}
	callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
		if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
			return err
		}
		read, err := readDecision(ctx, inv, target, source)
		if err != nil {
			return err
		}
		changed := identity.WithPrincipal(ctx, identity.System())
		if err := verifyDecision(changed, inv, read); !errors.Is(err, execution.ErrIdentityChanged) {
			t.Fatalf("foreign principal accepted: %v", err)
		}
		invalid.Store(true)
		if err := verifyDecision(ctx, inv, read); !errors.Is(err, stale) {
			t.Fatalf("provided stale evidence accepted: %v", err)
		}
		if read, err := readDecision(ctx, inv, target, source); read != nil || !errors.Is(err, stale) {
			t.Fatalf("cached stale evidence accepted: %v, %v", read, err)
		}
		return nil
	})
	if acquired.Load() != 1 || enrolled.Load() != 1 {
		t.Fatal("stale read reacquired or enrolled")
	}
}

func TestDecisionLateAcquisitionCannotEnrollOrIssueAfterCallbackExpiry(t *testing.T) {
	f := decisionFrameForTest(t, false)
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
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
		go func() {
			read, err := readDecision(ctx, inv, target, source)
			if read != nil {
				t.Error("late acquisition issued a read")
			}
			done <- err
		}()
		<-entered
		return nil // deliberately violates callback ownership; test joins below
	})
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expired callback accepted")
	}
	if acquired.Load() != 1 || enrolled.Load() != 0 {
		t.Fatal("late acquisition enrolled after expiry")
	}
}

func TestDecisionExpiredProviderCallbackCannotReachNextStage(t *testing.T) {
	for _, stage := range []string{"admission", "acquisition", "acquisition-check", "enrollment-check", "cached-check"} {
		t.Run(stage, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			entered, release := make(chan struct{}), make(chan struct{})
			type result struct {
				read *decisionRead
				err  error
			}
			done := make(chan result, 1)
			var checked atomic.Int32
			block := func() {
				close(entered)
				<-release
			}
			source.admit = func(context.Context) error {
				if stage == "admission" {
					block()
				}
				return nil
			}
			source.acquire = func(context.Context) (any, error) {
				acquired.Add(1)
				if stage == "acquisition" {
					block()
				}
				return new(int), nil
			}
			source.check = func(context.Context, any) error {
				n := checked.Add(1)
				if (stage == "acquisition-check" && n == 1) ||
					(stage == "enrollment-check" && n == 2) ||
					(stage == "cached-check" && n == 3) {
					block()
				}
				return nil
			}
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
					return err
				}
				if stage == "cached-check" {
					if _, err := readDecision(ctx, inv, target, source); err != nil {
						return err
					}
				}
				go func() {
					read, err := readDecision(ctx, inv, target, source)
					done <- result{read: read, err: err}
				}()
				<-entered
				return nil // expire this callback before the provider returns; joined below
			})
			close(release)
			got := <-done
			// Execution() no longer supplies a view after expiry; withState may
			// instead observe the retained view's closed callback directly.
			closed := errors.Is(got.err, ErrExecutionClosed) || errors.Is(got.err, ErrNoContext)
			if got.read != nil || !closed || !errors.Is(got.err, errDecisionRead) {
				t.Fatalf("expired read = %v, %v", got.read, got.err)
			}
			wantAcquired, wantChecked, wantEnrolled := int32(1), int32(0), int32(0)
			switch stage {
			case "admission":
				wantAcquired = 0
			case "acquisition-check":
				wantChecked = 1
			case "enrollment-check":
				wantChecked = 2
			case "cached-check":
				wantChecked, wantEnrolled = 3, 1 // only the earlier, live read enrolled
			}
			if acquired.Load() != wantAcquired || checked.Load() != wantChecked || enrolled.Load() != wantEnrolled {
				t.Fatalf("callbacks after expiry: acquired=%d, checked=%d, enrolled=%d; want %d, %d, %d",
					acquired.Load(), checked.Load(), enrolled.Load(), wantAcquired, wantChecked, wantEnrolled)
			}
		})
	}
}

func TestDecisionMidCallbackCancellationCannotReachNextStage(t *testing.T) {
	for _, stage := range []string{"admission", "acquisition", "acquisition-check", "enrollment-check"} {
		t.Run(stage, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			var checked atomic.Int32
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
					return err
				}
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				source.admit = func(context.Context) error {
					if stage == "admission" {
						cancel()
					}
					return nil
				}
				source.acquire = func(context.Context) (any, error) {
					acquired.Add(1)
					if stage == "acquisition" {
						cancel()
					}
					return new(int), nil
				}
				source.check = func(context.Context, any) error {
					n := checked.Add(1)
					if (stage == "acquisition-check" && n == 1) || (stage == "enrollment-check" && n == 2) {
						cancel()
					}
					return nil
				}
				read, err := readDecision(ctx, inv, target, source)
				if read != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, errDecisionRead) {
					t.Fatalf("canceled read = %v, %v", read, err)
				}
				return nil
			})
			wantAcquired, wantChecked := int32(1), int32(0)
			switch stage {
			case "admission":
				wantAcquired = 0
			case "acquisition-check":
				wantChecked = 1
			case "enrollment-check":
				wantChecked = 2
			}
			if acquired.Load() != wantAcquired || checked.Load() != wantChecked || enrolled.Load() != 0 {
				t.Fatalf("callbacks after cancellation: acquired=%d, checked=%d, enrolled=%d; want %d, %d, 0",
					acquired.Load(), checked.Load(), enrolled.Load(), wantAcquired, wantChecked)
			}
		})
	}
}

func TestDecisionCachedReadReenrollsAgainstCurrentOwner(t *testing.T) {
	f := decisionFrameForTest(t, false)
	var acquired, enrolled atomic.Int32
	target, source := decisionSourceForTest(&acquired, &enrolled)
	ownerMismatch := errors.New("another completion owner")
	callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
		if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
			return err
		}
		if _, err := readDecision(ctx, inv, target, source); err != nil {
			return err
		}
		otherSource := source
		otherSource.enroll = func(context.Context, any) error { return ownerMismatch }
		read, err := readDecision(ctx, inv, target, otherSource)
		if read != nil || !errors.Is(err, ownerMismatch) {
			t.Fatalf("cached read bypassed current owner: %v, %v", read, err)
		}
		return nil
	})
	if acquired.Load() != 1 || enrolled.Load() != 1 {
		t.Fatal("unexpected provider calls")
	}
}
