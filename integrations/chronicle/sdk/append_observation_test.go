// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"testing"

	"github.com/cratis/arc.go/correlation"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/metadata"
)

func TestAppendSubscriptionUsesExactCorrelationIncludingZero(t *testing.T) {
	for _, id := range []correlation.ID{{}, {1}} {
		var callback func(eventsequences.AppendNotification)
		calls, stops := 0, 0
		stop := subscribeAppends(func(notify func(eventsequences.AppendNotification)) func() {
			callback = notify
			return func() { stops++ }
		}, id, func(integration.CommitResult, error) { calls++ })
		for _, notificationID := range []metadata.CorrelationID{{}, {1}, {2}} {
			callback(eventsequences.AppendNotification{CorrelationID: notificationID})
		}
		if calls != 1 {
			t.Fatalf("correlation %v received %d notifications; want only its exact match", id, calls)
		}
		stop()
		stop()
		callback(eventsequences.AppendNotification{CorrelationID: metadata.CorrelationID([16]byte(id))})
		if stops != 1 || calls != 1 {
			t.Fatalf("stops = %d, callbacks after stop = %d", stops, calls)
		}
	}
}
