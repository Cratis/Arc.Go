//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import "context"

// JoinFailedWatchForTest is a test-only signal seam, not a runtime API. It waits
// for the actual reader/ordinary cursor cleanup before testing public fresh Open.
// CommandSucceeded(killCursors) alone precedes the reader's final ownership join.
func JoinFailedWatchForTest(ctx context.Context, w *Watcher, database string) error {
	w.mu.Lock()
	d := w.databases[database]
	w.mu.Unlock()
	if d == nil {
		return ErrConfiguration
	}
	if err := waitWatch(ctx, d.done); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if d.failure == nil {
		return ErrConfiguration
	}
	return d.cleanupErr
}
