// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package ginmount_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/recipes/ginmount"
	"github.com/cratis/arc.go/recipes/internal/fixture"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestGinCoordinatedShutdownDrainsActiveSSE(t *testing.T) {
	f := fixture.New(t, arc.Options{})
	server := fixture.Serve(t, ginmount.NewEngine(f.App))
	fixture.VerifyCoordinatedShutdown(t, f, server)
}

func TestGinMountPreservesArcBehavior(t *testing.T) {
	f := fixture.New(t, arc.Options{})
	server := fixture.Serve(t, ginmount.NewEngine(f.App))
	fixture.VerifyMount(t, f, server)
	t.Run("host routes keep working", func(t *testing.T) {
		if r := fixture.Do(t, server, http.MethodGet, "/healthz", "", nil); r.Status != http.StatusNoContent {
			t.Fatalf("got %d %q", r.Status, r.Body)
		}
	})
}
