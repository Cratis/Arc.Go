// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chimount_test

import (
	"net/http"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/recipes/chimount"
	"github.com/cratis/arc.go/recipes/internal/fixture"
)

func TestChiCoordinatedShutdownDrainsActiveSSE(t *testing.T) {
	f := fixture.New(t, arc.Options{})
	server := fixture.Serve(t, chimount.NewRouter(f.App))
	fixture.VerifyCoordinatedShutdown(t, f, server)
}

func TestChiMountPreservesArcBehavior(t *testing.T) {
	f := fixture.New(t, arc.Options{})
	server := fixture.Serve(t, chimount.NewRouter(f.App))
	fixture.VerifyMount(t, f, server)
	t.Run("host routes keep working", func(t *testing.T) {
		if r := fixture.Do(t, server, http.MethodGet, "/healthz", "", nil); r.Status != http.StatusNoContent {
			t.Fatalf("got %d %q", r.Status, r.Body)
		}
	})
}
