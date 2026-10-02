// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"context"
	"errors"
	"io"
)

// Closer receives the cooperative cleanup context and is preferred over io.Closer.
// Close must not assume that cancellation forcibly interrupts it.
type Closer interface{ Close(context.Context) error }

func (o *owner) cleanup(ctx context.Context) error {
	o.mu.Lock()
	values := o.values
	o.values = nil
	o.entries = nil
	o.mu.Unlock()
	var errs []error
	for i := len(values) - 1; i >= 0; i-- {
		errs = append(errs, closeValue(ctx, values[i]))
	}
	return errors.Join(errs...)
}
func closeValue(ctx context.Context, value ownedValue) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &Error{Operation: "close", Key: value.key, Kind: ErrCallbackPanicked, Panic: recovered}
		}
	}()
	switch closer := value.value.(type) {
	case Closer:
		err = closer.Close(ctx)
	case io.Closer:
		err = closer.Close()
	}
	if err != nil {
		return failure("close", value.key, nil, nil, err)
	}
	return nil
}
