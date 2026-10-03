// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
)

func TestAppendAttributionRequiresExactlyOneInFlightCommand(t *testing.T) {
	var log bytes.Buffer
	i := &Integration{options: Options{Logger: slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))}}
	coordinates := Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}
	var id correlation.ID
	first := i.beginObservation(coordinates, id)
	if !first.attribute(t.Context()) {
		t.Fatal("singleton zero correlation was not attributed")
	}
	second := i.beginObservation(coordinates, id)
	if first.attribute(t.Context()) || second.attribute(t.Context()) || first.attribute(t.Context()) {
		t.Fatal("shared correlation was attributed")
	}
	if strings.Count(log.String(), "Skipping ambiguous Chronicle append observation") != 1 {
		t.Fatalf("ambiguity log = %s", log.String())
	}
	second.release()
	second.release()
	if !first.attribute(t.Context()) {
		t.Fatal("singleton after completion was not attributed")
	}
	first.release()
	if len(i.observations) != 0 {
		t.Fatal("completed observations retained")
	}
}

func TestAppendAttributionSeparatesEveryKeyCoordinate(t *testing.T) {
	var i Integration
	base := Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}
	var id correlation.ID
	first := i.beginObservation(base, id)
	defer first.release()
	for _, other := range []observationKey{
		{Coordinates{Store: "other", Namespace: base.Namespace, Sequence: base.Sequence}, id},
		{Coordinates{Store: base.Store, Namespace: "other", Sequence: base.Sequence}, id},
		{Coordinates{Store: base.Store, Namespace: base.Namespace, Sequence: "other"}, id},
		{base, correlation.ID{1}},
	} {
		observation := i.beginObservation(other.coordinates, other.correlation)
		if !observation.attribute(t.Context()) || !first.attribute(t.Context()) {
			t.Fatalf("different key suppressed attribution: %+v", other)
		}
		observation.release()
	}
}

type observationOwner struct {
	observation *appendObservation
	attributed  bool
}

func (o *observationOwner) Commit(ctx context.Context) (CommitResult, error) {
	o.attributed = o.observation.attribute(ctx)
	return CommitResult{Report: commands.CompletionReport{Disposition: commands.NotCommitted}}, ErrInvalid
}
func (*observationOwner) Rollback() error { return nil }

func TestOwnerCommitNotificationsAreExcludedAndGuardIsReleasedOnFailure(t *testing.T) {
	var i Integration
	observation := i.beginObservation(Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}, correlation.ID{})
	defer observation.release()
	owner := &observationOwner{observation: observation}
	result, err := observation.commit(owner, t.Context())
	if owner.attributed || err != ErrInvalid || result.Report.Disposition != commands.NotCommitted {
		t.Fatalf("owner commit = %+v, %v; attributed = %v", result, err, owner.attributed)
	}
	if !observation.attribute(t.Context()) {
		t.Fatal("owner commit guard leaked after failure")
	}
}
