// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package fixture

import (
	"context"
	"errors"
	"net/http"
	"time"

	arc "github.com/cratis/arc.go"
)

// recipe:start host-shutdown

// ShutdownHost stops HTTP admission and Arc observations concurrently, then
// joins both. Each has a fresh five-second cleanup budget, not the canceled
// signal or request context. Business callbacks must honor cancellation.
func ShutdownHost(app *arc.Application, server *http.Server) error {
	httpContext, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHTTP()
	arcContext, cancelArc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelArc()
	httpStopped := make(chan error, 1)
	go func() {
		httpStopped <- server.Shutdown(httpContext)
	}()
	arcError := app.Shutdown(arcContext)
	return errors.Join(arcError, <-httpStopped)
}

// recipe:end
