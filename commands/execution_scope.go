// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// ExecutionScope participates once per root Execute. Begin runs in registration
// order before authorization; Complete runs in reverse order for every entered
// Begin, including a failing Begin. Completion uses a live bounded cleanup context
// but observes the already-failed forward result. This is not a rollback contract.
type ExecutionScope interface {
	Begin(context.Context, *Invocation) error
	Complete(context.Context, *Invocation, Result[any]) (Result[NoResponse], error)
}

// AddExecutionScope registers a lazy borrowed root-completion participant.
func (r *Registry) AddExecutionScope(name string, factory Factory[ExecutionScope], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	return addExtension(r, "participants", name, factory, keys, &r.participants)
}
