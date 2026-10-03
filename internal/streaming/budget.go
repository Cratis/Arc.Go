// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"errors"
	"sync"
)

// ErrByteCapacity indicates an explicit retained-memory limit, never a dropped
// frame or successful partial delivery.
var ErrByteCapacity = errors.New("streaming byte capacity exhausted")

// Budget bounds retained immutable frame or baseline bytes. Connection and
// application budgets are independent; callers acquire both before allocating
// retained copies. It does not limit allocations within application callbacks.
type Budget struct {
	mu          sync.Mutex
	limit, used int64
}

// NewBudget requires a positive byte ceiling and starts no work.
func NewBudget(limit int64) (*Budget, error) {
	if limit <= 0 {
		return nil, ErrControl
	}
	return &Budget{limit: limit}, nil
}

// Reservation owns bytes until Release. Copying a used reservation is forbidden.
// Release is idempotent and may be called concurrently.
type Reservation struct {
	once   sync.Once
	budget *Budget
	bytes  int64
}

// Acquire reserves before retained allocation. Rejection does not change usage.
func (b *Budget) Acquire(bytes int64) (*Reservation, error) {
	if b == nil || bytes < 0 {
		return nil, ErrControl
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytes > b.limit-b.used {
		return nil, ErrByteCapacity
	}
	b.used += bytes
	return &Reservation{budget: b, bytes: bytes}, nil
}

// Release returns the reserved bytes only after the retained value is discarded.
func (r *Reservation) Release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.budget.mu.Lock()
		r.budget.used -= r.bytes
		r.budget.mu.Unlock()
	})
}

// Used reports retained reservations, including in-flight writes and closing work.
func (b *Budget) Used() int64 { b.mu.Lock(); defer b.mu.Unlock(); return b.used }
