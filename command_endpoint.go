// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"net/http"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/internal/httptransport"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

func (a *Application) commandEndpoint(w http.ResponseWriter, r *http.Request, e metadata.Endpoint) {
	id := correlation.FromContext(r.Context())
	body, status, err := httptransport.ReadBody(w, r, a.options.HTTP.MaxBodyBytes)
	if err != nil {
		a.publish(w, r, status, commands.InvalidBody(id))
		return
	}
	registration, ok := a.commands.Lookup(e.Identity)
	if !ok {
		w.WriteHeader(500)
		return
	}
	var value any
	err = boundary.Call(r.Context(), func(context.Context) error { var err error; value, err = registration.Decode(body); return err })
	if err != nil {
		var decode *commands.DecodeError
		if errors.As(err, &decode) {
			a.publish(w, r, 400, commands.InvalidBody(id))
		} else {
			a.publish(w, r, 500, commands.FromError[any](id, err))
		}
		return
	}
	var options []commands.ExecuteOptions
	if severity, ok := validation.AllowedFromHeader(headerValues(r.Header, validation.AllowedSeverityHeader)); ok {
		options = []commands.ExecuteOptions{{AllowedSeverity: &severity}}
	}
	if e.ValidateOnly {
		result, _ := a.commands.Validate(r.Context(), value, options...)
		a.publish(w, r, result.StatusCode(), result)
		return
	}
	result, _ := a.commands.Execute(r.Context(), value, options...)
	a.publish(w, r, result.StatusCode(), result)
}
