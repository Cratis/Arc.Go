// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidArgument identifies invalid openers/callbacks/contexts or negative cleanup timeouts.
var ErrInvalidArgument = errors.New("invalid execution argument")

// RunWithResources executes synchronously with explicit operation metadata and
// fresh owned resources. The callback receives a non-closing view. Completion of
// the callback precedes disposal; disposal does not imply commit. Cleanup uses a
// bounded WithoutCancel context (zero means 30 seconds). Errors and PanicError
// diagnostics are joined; no goroutines are launched.
func RunWithResources(ctx context.Context, open OpenResources, metadata Metadata, cleanupTimeout time.Duration, call func(context.Context, *Scope) error) (err error) {
	if ctx == nil || call == nil || cleanupTimeout < 0 {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, err = NewContext(ctx, metadata)
	if err != nil {
		return err
	}
	scope, err := OpenScope(ctx, open)
	if err != nil {
		return err
	}
	if cleanupTimeout == 0 {
		cleanupTimeout = 30 * time.Second
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		err = errors.Join(err, scope.Close(cleanup))
	}()
	return scope.Use(ctx, call)
}
