// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"
	"runtime/debug"
)

// PanicError records an application callback panic for local diagnostics only.
// Neither Value nor Stack is safe to expose to a client.
type PanicError struct {
	// Value is the recovered panic value.
	Value any
	// Stack is a snapshot of the panicking goroutine's stack.
	Stack []byte
}

// Error returns safe text without the panic value or stack.
func (e *PanicError) Error() string { return "application callback panicked" }

func invoke(ctx context.Context, call func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{Value: value, Stack: debug.Stack()}
		}
		err = errors.Join(err, ctx.Err())
	}()
	return call()
}
