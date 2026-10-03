// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/tenancy"
)

func revision(n uint64) *streaming.Revision { r := streaming.Revision(n); return &r }
func states(t *testing.T, options streaming.SubscriptionOptions) *streaming.Subscriptions {
	t.Helper()
	s, err := streaming.NewSubscriptions(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func subscribe(t *testing.T, s *streaming.Subscriptions, id string, r *streaming.Revision) *streaming.Operation {
	t.Helper()
	o, _, err := s.Subscribe(id, r)
	if err != nil || o == nil {
		t.Fatalf("subscribe = %v %v", o, err)
	}
	return o
}

func TestRevisionLexicalRangeAndNullableDTO(t *testing.T) {
	for _, token := range []string{`1`, `9007199254740991`} {
		var envelope struct {
			Revision *streaming.Revision `json:"revision,omitempty"`
		}
		if err := json.Unmarshal([]byte(`{"revision":`+token+`}`), &envelope); err != nil || envelope.Revision == nil || !envelope.Revision.Valid() {
			t.Fatalf("%s: %+v %v", token, envelope, err)
		}
	}
	for _, token := range []string{`0`, `-1`, `1.0`, `1e0`, `"1"`, `true`, `9007199254740992`, `18446744073709551616`, `01`, `+1`} {
		var r streaming.Revision
		if err := json.Unmarshal([]byte(token), &r); err == nil {
			t.Fatalf("accepted %s", token)
		}
	}
	for _, body := range []string{`{}`, `{"revision":null}`} {
		var envelope struct {
			Revision *streaming.Revision `json:"revision,omitempty"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil || envelope.Revision != nil {
			t.Fatalf("legacy %s: %v", body, err)
		}
	}
	for _, r := range []streaming.Revision{0, streaming.MaxRevision + 1} {
		if _, err := json.Marshal(r); !errors.Is(err, streaming.ErrRevision) {
			t.Fatal(err)
		}
	}
}

func TestUnsubscribeBeforeDelayedSubscribeAndEqualRevision(t *testing.T) {
	s := states(t, streaming.SubscriptionOptions{})
	if err := s.Unsubscribe("q", revision(2)); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*streaming.Revision{nil, revision(1), revision(2)} {
		o, _, err := s.Subscribe("q", r)
		if err != nil || o != nil {
			t.Fatalf("stale subscribe %v %v", o, err)
		}
	}
	o := subscribe(t, s, "q", revision(3))
	if err := s.Unsubscribe("q", revision(2)); err != nil || !s.Owns(o) {
		t.Fatal("stale unsubscribe changed owner")
	}
	if err := s.Unsubscribe("q", revision(3)); err != nil || s.Owns(o) || !errors.Is(o.Context().Err(), context.Canceled) {
		t.Fatal("equal unsubscribe did not cancel")
	}
	if err := s.Unsubscribe("q", revision(3)); err != nil {
		t.Fatal(err)
	}
	s.Joined(o)
	ids, operations := s.Counts()
	if ids != 1 || operations != 0 {
		t.Fatalf("counts %d %d", ids, operations)
	}
	o, _, err := s.Subscribe("q", revision(3))
	if err != nil || o != nil {
		t.Fatal("equal revision resurrected")
	}
}

func TestLegacyUpgradeAndLateCompletionCannotAffectSuccessor(t *testing.T) {
	s := states(t, streaming.SubscriptionOptions{})
	legacy := subscribe(t, s, "q", nil)
	upgrade, replaced, err := s.Subscribe("q", revision(1))
	if err != nil || replaced != legacy || upgrade == nil || s.Owns(legacy) || !s.Owns(upgrade) {
		t.Fatal("upgrade did not replace temporary legacy owner")
	}
	if !errors.Is(legacy.Context().Err(), context.Canceled) {
		t.Fatal("replaced legacy worker not canceled")
	}
	for _, r := range []*streaming.Revision{nil, revision(1)} {
		o, _, err := s.Subscribe("q", r)
		if err != nil || o != nil {
			t.Fatal("legacy/duplicate replaced revision owner")
		}
	}
	if err := s.Unsubscribe("q", nil); err != nil || !s.Owns(upgrade) {
		t.Fatal("legacy unsubscribe overrode aware owner")
	}
	s.Terminate(legacy)
	s.Joined(legacy)
	if !s.Owns(upgrade) {
		t.Fatal("late legacy error terminated successor")
	}
	latest := subscribe(t, s, "q", revision(2))
	s.Joined(latest) // New opening finishes before the replaced old opening.
	s.Joined(upgrade)
	o, _, err := s.Subscribe("q", revision(2))
	if err != nil || o != nil {
		t.Fatal("late old completion lost high-water mark")
	}
}

func TestLegacyReplacementReturnsWriterFenceOwner(t *testing.T) {
	s := states(t, streaming.SubscriptionOptions{})
	first := subscribe(t, s, "q", nil)
	next, fence, err := s.Subscribe("q", nil)
	if err != nil || next == nil || fence != first || next == first || s.Owns(first) {
		t.Fatal("legacy replacement lost token fence")
	}
	s.Joined(first)
	if !s.Owns(next) {
		t.Fatal("legacy completion affected replacement")
	}
	s.Joined(next)
	ids, operations := s.Counts()
	if ids != 0 || operations != 0 {
		t.Fatalf("legacy state leaked %d %d", ids, operations)
	}
}

func TestTombstoneCapacityNeverEvictsOrderingProtection(t *testing.T) {
	s := states(t, streaming.SubscriptionOptions{MaxIDs: 1})
	if err := s.Unsubscribe("old", revision(4)); err != nil {
		t.Fatal(err)
	}
	if err := s.Unsubscribe("new", revision(1)); !errors.Is(err, streaming.ErrCapacity) {
		t.Fatal(err)
	}
	if _, _, err := s.Subscribe("new", revision(1)); !errors.Is(err, streaming.ErrCapacity) {
		t.Fatal(err)
	}
	o, _, err := s.Subscribe("old", revision(4))
	if err != nil || o != nil {
		t.Fatal("capacity exhaustion resurrected old revision")
	}
	o = subscribe(t, s, "old", revision(5))
	s.Joined(o)
}

func TestRepeatedReplacementCountsUnjoinedRetiredWorkers(t *testing.T) {
	s := states(t, streaming.SubscriptionOptions{MaxOperations: 2})
	first := subscribe(t, s, "q", revision(1))
	second := subscribe(t, s, "q", revision(2))
	if _, _, err := s.Subscribe("q", revision(3)); !errors.Is(err, streaming.ErrCapacity) {
		t.Fatal(err)
	}
	if !s.Owns(second) {
		t.Fatal("rejected replacement changed owner")
	}
	select {
	case <-first.Done():
		t.Fatal("cancellation pretended to join")
	default:
	}
	s.Joined(first)
	third := subscribe(t, s, "q", revision(3))
	s.Joined(second)
	s.Joined(third)
	s.Joined(third)
}

func TestDrainJoinsOpeningActiveAndRetiredTokens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := states(t, streaming.SubscriptionOptions{})
		first := subscribe(t, s, "q", revision(1))
		second := subscribe(t, s, "q", revision(2))
		other := subscribe(t, s, "other", nil)
		list := s.Drain()
		if len(list) != 3 {
			t.Fatal(len(list))
		}
		if _, _, err := s.Subscribe("new", revision(1)); !errors.Is(err, streaming.ErrDraining) {
			t.Fatal(err)
		}
		for _, o := range []*streaming.Operation{first, second, other} {
			if s.Owns(o) || !errors.Is(o.Context().Err(), context.Canceled) {
				t.Fatal("drain left admitted worker")
			}
			select {
			case <-o.Done():
				t.Fatal("drain claimed cleanup joined")
			default:
			}
		}
		for _, o := range list {
			s.Joined(o)
			<-o.Done()
		}
		if remaining := s.Drain(); len(remaining) != 0 {
			t.Fatal("joined tokens retained")
		}
	})
}

func TestForeignTokensCannotCancelAnotherConnection(t *testing.T) {
	one, two := states(t, streaming.SubscriptionOptions{}), states(t, streaming.SubscriptionOptions{})
	o := subscribe(t, one, "q", revision(1))
	two.Terminate(o)
	two.Joined(o)
	if !one.Owns(o) {
		t.Fatal("foreign registry canceled token")
	}
	one.Joined(o)
}

func TestConnectionOwnershipUsesStableSubjectAndTenantPresence(t *testing.T) {
	ctx := context.Background()
	p := identity.NewPrincipal(identity.PrincipalData{ID: "subject", Name: "display", AuthenticationType: "verified"})
	ctx = identity.WithPrincipal(ctx, p)
	owner, err := streaming.NewConnectionOwner(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	changedDisplay := identity.WithPrincipal(context.Background(), identity.NewPrincipal(identity.PrincipalData{ID: "subject", Name: "changed", AuthenticationType: "other verified"}))
	same, err := streaming.NewConnectionOwner(changedDisplay, "ignored")
	if err != nil || !owner.Equal(same) {
		t.Fatal("mutable display/adapter name changed subject ownership")
	}
	for _, changed := range []context.Context{
		identity.WithPrincipal(context.Background(), identity.System()),
		identity.WithPrincipal(context.Background(), identity.Principal{}),
		tenancy.WithTenant(ctx, tenancy.ID{}),
		tenancy.WithTenant(ctx, tenancy.Default()),
	} {
		other, err := streaming.NewConnectionOwner(changed, "anonymous evidence")
		if err != nil || owner.Equal(other) {
			t.Fatal("wrong subject/authentication/tenant accepted")
		}
	}
	anonymous, err := streaming.NewConnectionOwner(context.Background(), "cookie1")
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := streaming.NewConnectionOwner(context.Background(), "cookie2")
	if err != nil || anonymous.Equal(wrong) {
		t.Fatal("anonymous owners conflated")
	}
	if _, err := streaming.NewConnectionOwner(context.Background(), ""); err == nil {
		t.Fatal("missing evidence accepted")
	}
}

func FuzzRevision(f *testing.F) {
	for _, seed := range []string{"1", "9007199254740991", "1e0", "null", "-1", "0", "9007199254740992"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, token string) {
		var r streaming.Revision
		if err := json.Unmarshal([]byte(token), &r); err != nil {
			return
		}
		if !r.Valid() {
			t.Fatal("invalid accepted revision")
		}
		encoded, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var roundtrip streaming.Revision
		if err := json.Unmarshal(encoded, &roundtrip); err != nil || roundtrip != r {
			t.Fatal("revision roundtrip")
		}
	})
}
