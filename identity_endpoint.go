// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
)

func (a *Application) identityEndpoint(w http.ResponseWriter, r *http.Request) {
	principal, _ := identity.PrincipalFrom(r.Context())
	if !principal.IsAuthenticated() {
		w.WriteHeader(401)
		return
	}
	var value any
	var err error
	if a.details.scoped {
		value, err = a.withResources(r.Context(), a.details.provide)
	} else {
		err = boundary.Call(r.Context(), func(ctx context.Context) error { var err error; value, err = a.details.provide(ctx, nil); return err })
	}
	if err != nil {
		if errors.Is(err, identity.ErrDetailsDenied) {
			w.WriteHeader(403)
		} else {
			a.hostFailure(r.Context(), "Identity provider failed", err)
			w.WriteHeader(500)
		}
		return
	}
	a.publish(w, r, 200, value)
}
func (a *Application) withResources(ctx context.Context, call func(context.Context, *execution.Scope) (any, error)) (any, error) {
	scope, err := execution.OpenScope(ctx, a.options.OpenResources)
	if err != nil {
		return nil, err
	}
	var value any
	err = scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		var err error
		value, err = call(ctx, view)
		return err
	})
	cleanup, cancel, cleanupErr := boundary.CleanupContext(ctx, a.options.CleanupTimeout)
	if cleanupErr == nil {
		cleanupErr = scope.Close(cleanup)
		cancel()
	}
	return value, errors.Join(err, cleanupErr)
}
func (a *Application) hostFailure(ctx context.Context, message string, err error, levels ...slog.Level) {
	if a.options.Logger != nil {
		level := slog.LevelError
		if len(levels) > 0 {
			level = levels[0]
		}
		a.options.Logger.Log(ctx, level, message, "error", err)
	}
}
