// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"errors"

	"github.com/cratis/arc.go/serialization"
)

// ErrBaselineCapacity identifies retained collection state exceeding its byte limit.
// Delivery fails explicitly; no silent coalescing or baseline eviction occurs.
var ErrBaselineCapacity = errors.New("observable collection baseline capacity exhausted")

type collectionTransfer struct {
	shape     *collectionShape
	mode      TransferMode
	limit     int64
	reserve   func(int64) (func(), error)
	delivered *collectionSnapshot
	release   func()
}

func (t *collectionTransfer) close() {
	t.delivered = nil
	if t.release != nil {
		t.release()
		t.release = nil
	}
}

// prepare creates a candidate transaction, not delivered state. Commit is called
// only after the delivery callback acknowledges success; discard covers every
// failure, including panics. The delivered snapshot never aliases callback data.
func (t *collectionTransfer) prepare(result Result[any], hints collectionHints) (Result[any], func(), func(), error) {
	noop := func() {}
	details := result.Details()
	details.ChangeSet = nil
	data, present := result.Data()
	result = NewResult(details, serialization.Optional[any]{})
	if present {
		result = NewResult(details, serialization.Some(data))
	}
	if !result.IsSuccess() || t.mode == Full || t.shape == nil || t.shape.identity == nil || !present || nilValue(data) {
		return result, noop, noop, nil
	}
	candidate, err := t.shape.freeze(data)
	if err != nil {
		return result, noop, noop, err
	}
	if candidate.bytes > t.limit {
		return result, noop, noop, ErrBaselineCapacity
	}
	// Retain only scalar continuity metadata; source change IDs are never retained
	// after preparation. Hints are resolved against intercepted serialized items.
	candidate.hints = hints
	release := noop
	if t.reserve != nil {
		release, err = t.reserve(candidate.bytes)
		if err != nil {
			return result, noop, noop, err
		}
		if release == nil {
			return result, noop, noop, ErrInvalidRegistration
		}
	}
	prepared := false
	defer func() {
		if !prepared {
			release()
		}
	}()
	if t.mode == Legacy || t.delivered != nil {
		details.ChangeSet = t.shape.knownChanges(t.delivered, candidate)
		result = NewResult(details, serialization.Some(data))
		if t.mode == Delta {
			result = NewResult(details, serialization.Optional[any]{})
		}
	}
	candidate.hints.changes = nil
	// Exactly one commit or discard, serially owned by Observation.Run.
	finished := false
	discard := func() {
		if !finished {
			finished = true
			release()
		}
	}
	commit := func() {
		if finished {
			return
		}
		finished = true
		t.close()
		t.delivered, t.release = candidate, release
	}
	prepared = true
	return result, commit, discard, nil
}
