// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package correlation_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
)

func TestCorrelationParsingAndReplacement(t *testing.T) {
	text := "12345678-abcd-4abc-8def-123456789012"
	id, err := correlation.Parse("  " + strings.ToUpper(text) + "\n")
	if err != nil || id.String() != text {
		t.Fatal(id, err)
	}
	var alias concepts.UUID = id
	if alias != id {
		t.Fatal("alias")
	}
	for _, invalid := range []string{"", "bad", "00000000-0000-0000-0000-000000000000", "{12345678-abcd-4abc-8def-123456789012}"} {
		if _, err := correlation.Parse(invalid); !errors.Is(err, correlation.ErrInvalidID) {
			t.Fatal(err)
		}
		got, err := correlation.Normalize(invalid)
		if err != nil || got.IsZero() || got.String()[14] != '4' || !strings.ContainsRune("89ab", rune(got.String()[19])) {
			t.Fatal(got, err)
		}
	}
	if got, err := correlation.Normalize(text); err != nil || got != id {
		t.Fatal(got, err)
	}
}
func TestCorrelationContextPrecedence(t *testing.T) {
	root := context.Background()
	if !correlation.FromContext(root).IsZero() {
		t.Fatal("generated on read")
	}
	id, err := correlation.Parse("12345678-abcd-4abc-8def-123456789012")
	if err != nil {
		t.Fatal(err)
	}
	parent := correlation.WithID(root, id)
	if got, err := correlation.Resolve(parent, "bad"); err != nil || got != id {
		t.Fatal(got, err)
	}
	supplied := "abcdef12-abcd-4abc-8def-123456789012"
	if got, err := correlation.Resolve(parent, supplied); err != nil || got.String() != supplied {
		t.Fatal(got, err)
	}
	child := correlation.WithID(parent, correlation.ID{})
	if !correlation.FromContext(child).IsZero() || correlation.FromContext(parent) != id {
		t.Fatal("shadow")
	}
	if got, err := correlation.Resolve(child, ""); err != nil || got.IsZero() || got == id {
		t.Fatal(got, err)
	}
}
func ExampleResolve() {
	id, err := correlation.Resolve(context.Background(), "12345678-ABCD-4ABC-8DEF-123456789012")
	if err != nil {
		panic(err)
	}
	fmt.Println(id)
	// Output: 12345678-abcd-4abc-8def-123456789012
}
func FuzzCorrelationParse(f *testing.F) {
	for _, seed := range []string{"", "bad", "00000000-0000-0000-0000-000000000000", " 12345678-ABCD-4ABC-8DEF-123456789012 "} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		id, err := correlation.Parse(text)
		if err != nil {
			return
		}
		if id.IsZero() {
			t.Fatal("zero accepted")
		}
		got, err := correlation.Parse(id.String())
		if err != nil || got != id {
			t.Fatal("round trip", err)
		}
	})
}
