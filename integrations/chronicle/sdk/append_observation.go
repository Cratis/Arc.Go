// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"sync"

	"github.com/cratis/arc.go/correlation"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/metadata"
)

func (a *adapter) Subscribe(ctx context.Context, c integration.Coordinates, id correlation.ID, notify func(integration.CommitResult, error)) (func(), error) {
	sequence, err := a.sequence(ctx, c)
	if err != nil {
		return nil, err
	}
	return subscribeAppends(sequence.OnAppend, id, notify), nil
}

func subscribeAppends(onAppend func(func(eventsequences.AppendNotification)) func(), id correlation.ID, notify func(integration.CommitResult, error)) func() {
	var mu sync.Mutex
	var callbacks sync.WaitGroup
	closed := false
	unsubscribe := onAppend(func(n eventsequences.AppendNotification) {
		// Zero is an exact correlation, never a broadcast. The integration owns
		// in-flight ambiguity and owner-commit suppression for this subscription.
		if n.CorrelationID != metadata.CorrelationID([16]byte(id)) {
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		callbacks.Add(1)
		mu.Unlock()
		defer callbacks.Done()
		notify(mapResult(n.Result), n.Err)
	})
	var once sync.Once
	return func() { once.Do(func() { mu.Lock(); closed = true; mu.Unlock(); unsubscribe(); callbacks.Wait() }) }
}
