// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"testing"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/metadata"
)

func TestAppendSubscriptionRequiresExactNonzeroOriginNotCorrelation(t *testing.T) {
	origin, other := eventsequences.NewOrigin(), eventsequences.NewOrigin()
	for _, selected := range []eventsequences.Origin{{}, origin} {
		var callback func(eventsequences.AppendNotification)
		calls, stops := 0, 0
		stop := subscribeAppends(func(notify func(eventsequences.AppendNotification)) func() {
			callback = notify
			return func() { stops++ }
		}, selected, func(integration.CommitResult, error) { calls++ })
		for _, notificationOrigin := range []eventsequences.Origin{{}, origin, other} {
			for _, id := range []metadata.CorrelationID{{}, {1}} {
				callback(eventsequences.AppendNotification{Origin: notificationOrigin, CorrelationID: id})
			}
		}
		want := 2
		if selected == (eventsequences.Origin{}) {
			want = 0
		}
		if calls != want {
			t.Fatalf("origin %v received %d notifications; want %d", selected, calls, want)
		}
		stop()
		stop()
		callback(eventsequences.AppendNotification{Origin: selected})
		if stops != 1 || calls != want {
			t.Fatalf("stops = %d, callbacks after stop = %d", stops, calls)
		}
	}
}
