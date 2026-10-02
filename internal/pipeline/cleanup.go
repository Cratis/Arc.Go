// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline

import (
	"context"
	"time"

	"github.com/cratis/arc.go/execution"
)

// CleanupContext detaches cancellation, preserving values, for synchronous
// cooperative cleanup only. Zero means 30 seconds. The caller must cancel it.
func CleanupContext(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc, error) {
	if ctx == nil || budget < 0 {
		return nil, nil, execution.ErrInvalidArgument
	}
	if budget == 0 {
		budget = 30 * time.Second
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	return cleanup, cancel, nil
}
