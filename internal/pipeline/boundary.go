// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package pipeline provides shared application callback and failure boundaries.
// It does not implement command/query stage ordering.
package pipeline

import (
	"context"
	"errors"
	"runtime/debug"

	"github.com/cratis/arc.go/execution"
)

// Call checks cancellation around an application callback and captures only that
// callback's panic. Never use it around framework dispatch or result processing.
func Call(ctx context.Context, callback func(context.Context) error) (err error) {
	if ctx == nil || callback == nil {
		return execution.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if value := recover(); value != nil {
			err = &execution.PanicError{Value: value, Stack: debug.Stack()}
		}
		err = errors.Join(err, ctx.Err())
	}()
	return callback(ClearDiagnostics(ctx))
}
