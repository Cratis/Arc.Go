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
		app.ServeHTTP(immediateWriter{c.Writer}, c.Request)
		// Gin appends "404 page not found" to a NoRoute response that wrote
		// no body. Commit Arc's status so its empty 404 stays empty.
		c.Writer.WriteHeaderNow()
	})
	return engine
}

// immediateWriter restores net/http's immediate header commitment. Gin defers
// WriteHeader; Arc's wrappers hide Gin's WriteHeaderNow from WebSocket libraries,
// so an unadapted 101 would remain buffered when the connection is hijacked.
// Embedding keeps Gin's flush/hijack support and response accounting intact.
type immediateWriter struct{ gin.ResponseWriter }

func (w immediateWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	w.WriteHeaderNow()
}

func (w immediateWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// recipe:end
