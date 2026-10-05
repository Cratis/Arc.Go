// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package echomount delegates Echo requests to an Arc application.
package echomount

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// recipe:start echo-mount

// NewEcho returns an Echo instance that serves host routes and forwards every
// other request, unchanged, to the started Arc application.
func NewEcho(app http.Handler) *echo.Echo {
	e := echo.New()
	e.GET("/healthz", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	// Echo v5's Any matches every method, including QUERY.
	e.Any("/*", echo.WrapHandler(app))
	return e
}

// recipe:end
