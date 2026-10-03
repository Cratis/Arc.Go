// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline

import (
	"context"
	"time"

	"github.com/cratis/arc.go/execution"
)

type transportReceiptKey struct{}

// ForwardReceipt installs a receipt for only the immediate pipeline invocation.
func ForwardReceipt(ctx context.Context, received time.Time) context.Context {
	return context.WithValue(execution.WithReceivedAt(ctx, received), transportReceiptKey{}, received)
}

// Receipt consumes the forwarding marker before callbacks. Nested operations
// inherit correlation but capture a fresh receipt through their own clock.
func Receipt(ctx context.Context, clock func() time.Time) (context.Context, time.Time, error) {
	received, forwarded := ctx.Value(transportReceiptKey{}).(time.Time)
	ctx = context.WithValue(ctx, transportReceiptKey{}, nil)
	if !forwarded {
		if err := Call(ctx, func(context.Context) error { received = clock(); return nil }); err != nil {
			return ctx, time.Time{}, err
		}
	}
	return execution.WithReceivedAt(ctx, received), received, nil
}
