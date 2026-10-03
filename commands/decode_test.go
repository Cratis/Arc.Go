// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/validation"
)

func TestCommandDecodeFreshAndSafe(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[*Rename](&r, commands.Handle(func(c *Rename, _ context.Context) (string, error) { return c.Name, nil })))
	p := build(t, &r, commands.PipelineOptions{})
	registration, _ := p.Lookup("Rename")
	a, err := registration.Decode([]byte(`{"name":"Ada"}`))
	must(t, err)
	b, err := registration.Decode([]byte(`{"name":"Ada"}`))
	must(t, err)
	if a.(*Rename) == b.(*Rename) {
		t.Fatal("decode reused input")
	}
	for _, body := range []string{"", "null", "[]", "42", `{"name":true}`, `{"name":"secret"} {}`, "{"} {
		_, err := registration.Decode([]byte(body))
		var failure validation.Failure
		var decode *commands.DecodeError
		if !errors.As(err, &failure) || !errors.As(err, &decode) || decode.Cause == nil {
			t.Fatalf("unsafe decode %q: %v", body, err)
		}
		if got := failure.ValidationResults(); len(got) != 1 || got[0].Reason != validation.MalformedRequest || got[0].Message != "The request body could not be read or is not valid for this command." {
			t.Fatal(got)
		}
	}
}
func FuzzCommandDecode(f *testing.F) {
	var r commands.Registry
	if err := commands.Register(&r, commands.Handle(Rename.Handle)); err != nil {
		f.Fatal(err)
	}
	p, err := r.Build(commands.PipelineOptions{})
	if err != nil {
		f.Fatal(err)
	}
	registration, _ := p.Lookup("Rename")
	for _, seed := range []string{`{"name":"Ada"}`, "null", `{"name":2}`, "{"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		value, err := registration.Decode(body)
		if err != nil {
			var failure validation.Failure
			if !errors.As(err, &failure) {
				t.Fatal(err)
			}
			return
		}
		if _, ok := value.(Rename); !ok {
			t.Fatalf("type %T", value)
		}
	})
}
