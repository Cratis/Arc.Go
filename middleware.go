// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"net/http"

	boundary "github.com/cratis/arc.go/internal/pipeline"
)

// Middleware composes a handler once at Build. First registered is outermost.
// Constructors must not perform startup work; use Lifecycle for owned work.
// Middleware is trusted code; requests already carry correlation and receipt.
type Middleware func(http.Handler) http.Handler

// Use appends middleware, rejecting nil values and mutation after Build.
func (b *Builder) Use(middleware ...Middleware) error {
	if b.attempted {
		return ErrFrozen
	}
	for _, m := range middleware {
		if m == nil {
			return ErrInvalidOptions
		}
	}
	b.middleware = append(b.middleware, middleware...)
	return nil
}
func (a *Application) composeMiddleware(middleware []Middleware) error {
	var handler http.Handler = http.HandlerFunc(a.authenticateAndDispatch)
	for i := len(middleware) - 1; i >= 0; i-- {
		previous := handler
		if err := boundary.Call(context.Background(), func(context.Context) error {
			handler = middleware[i](previous)
			if nilValue(handler) {
				return ErrInvalidOptions
			}
			return nil
		}); err != nil {
			return &ConfigurationError{Component: "middleware", Cause: err}
		}
	}
	a.requestHandler = handler
	return nil
}
