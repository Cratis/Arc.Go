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
