// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"sync"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/internal/appendorigin"
	"github.com/cratis/chronicle.go/eventsequences"
)

// ResolveAppendOrigin reads the current Arc callback snapshot. Executing frames
// with a typed token select it; validation and Arc frames without a token select
// zero to mask inherited SDK origins. Outside Arc it defers to OriginFrom.
// Configure chronicle.WithAppendOriginResolver(ResolveAppendOrigin) when creating
// the client, before sdk.New. It retains no context or execution capability.
func ResolveAppendOrigin(ctx context.Context) (eventsequences.Origin, bool, error) {
	command, found := commands.ContextFrom(ctx)
	if !found {
		return eventsequences.Origin{}, false, nil
	}
	if !command.IsValidationOnly() {
		value, _ := command.Values().Get(appendorigin.Name)
		if origin, ok := value.(eventsequences.Origin); ok {
			return origin, true, nil
		}
	}
	return eventsequences.Origin{}, true, nil
}

func (*adapter) NewAppendOrigin() any { return eventsequences.NewOrigin() }

func (a *adapter) Subscribe(ctx context.Context, c integration.Coordinates, _ correlation.ID, notify func(integration.CommitResult, error)) (func(), error) {
	origin, ok := appendorigin.From(ctx).(eventsequences.Origin)
	if !ok || origin == (eventsequences.Origin{}) {
		return nil, integration.ErrInvalid
	}
	ctx = eventsequences.WithOrigin(ctx, origin)
	sequence, err := a.sequence(ctx, c)
	if err != nil {
		return nil, err
	}
	return subscribeAppends(sequence.OnAppend, eventsequences.OriginFrom(ctx), notify), nil
}

func subscribeAppends(onAppend func(func(eventsequences.AppendNotification)) func(), origin eventsequences.Origin, notify func(integration.CommitResult, error)) func() {
	var mu sync.Mutex
	var callbacks sync.WaitGroup
	closed := false
	unsubscribe := onAppend(func(n eventsequences.AppendNotification) {
		// Correlation never determines ownership. Zero is always unattributed;
		// unit commits carry a different SDK-owned origin and cannot match.
		if origin == (eventsequences.Origin{}) || n.Origin != origin {
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
