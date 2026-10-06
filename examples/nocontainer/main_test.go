// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/identity"
)

func Example() {
	main()
	// Output:
	// authorized: true
	// validation findings: 0
	// hello, Arc
	// resources closed: true
}

func TestDenialSkipsWorkAndClosesResources(t *testing.T) {
	var output bytes.Buffer
	err := run(context.Background(), &output, identity.System(), "Arc")
	if !errors.Is(err, authorization.ErrDenied) {
		t.Fatal(err)
	}
	if got := output.String(); got != "authorized: false\nresources closed: true\n" {
		t.Fatal(got)
	}
}

func TestInvalidInputSkipsWorkAndClosesResources(t *testing.T) {
	var output bytes.Buffer
	err := run(context.Background(), &output, identity.System("Greeter"), " ")
	if !errors.Is(err, errInvalidName) {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "validation findings: 1\n") || strings.Contains(got, "hello,") || !strings.HasSuffix(got, "resources closed: true\n") {
		t.Fatal(got)
	}
}
