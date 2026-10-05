// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package chimount mounts an Arc application in a Chi router.
package chimount

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// recipe:start chi-mount

// NewRouter returns a Chi router that serves host routes and forwards every
// other request, unchanged, to the started Arc application.
func NewRouter(app http.Handler) chi.Router {
	// Chi v5.3.1 and later route QUERY natively, so this is a no-op there.
	// Earlier versions answer QUERY with Chi's own 405 unless it is registered
	// before Mount. Registration is process-global.
	chi.RegisterMethod("QUERY")

	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.Mount("/", app)
	return router
}

// recipe:end
