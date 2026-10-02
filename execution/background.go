// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"
	"time"

	"github.com/cratis/arc.go/services"
)

// ErrInvalidArgument identifies nil providers/callbacks/contexts or negative cleanup timeouts.
var ErrInvalidArgument = errors.New("invalid execution argument")

// Run executes synchronously in a fresh scope and always closes that scope.
// Supply an application/job lifetime context, not an accidentally detached request.
// Explicit metadata replaces parent authority; execution retains caller cancellation.
// Cleanup uses a WithoutCancel child with a bounded timeout (zero means 30 seconds).
// Timeouts are cooperative; no detached goroutine is created. Callback and cleanup
// errors are joined. Callback panics are re-panicked after attempting cleanup.
func Run(ctx context.Context, provider *services.Provider, metadata Metadata, cleanupTimeout time.Duration, call func(context.Context, *services.Scope) error) (err error) {
	if ctx == nil || provider == nil || call == nil || cleanupTimeout < 0 {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, err = NewContext(ctx, metadata)
	if err != nil {
		return err
	}
	scope, err := provider.NewScope(ctx)
	if err != nil {
		return err
	}
	if cleanupTimeout == 0 {
		cleanupTimeout = 30 * time.Second
	}
	defer func() {
		recovered := recover()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		cleanupErr := scope.Close(cleanupCtx)
		if recovered != nil {
			panic(recovered)
		}
		err = errors.Join(err, cleanupErr)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	err = call(ctx, scope)
	return errors.Join(err, ctx.Err())
}
