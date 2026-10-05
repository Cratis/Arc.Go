// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package ginmount forwards unmatched Gin requests to an Arc application.
package ginmount

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// recipe:start gin-mount

// NewEngine returns a Gin engine that serves host routes and forwards every
// unmatched request, unchanged, to the started Arc application.
func NewEngine(app http.Handler) *gin.Engine {
	engine := gin.New()
	engine.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	engine.NoRoute(func(c *gin.Context) {
		app.ServeHTTP(c.Writer, c.Request)
		// Gin appends "404 page not found" to a NoRoute response that wrote
		// no body. Commit Arc's status so its empty 404 stays empty.
		c.Writer.WriteHeaderNow()
	})
	return engine
}

// recipe:end
