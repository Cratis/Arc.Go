// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/cratis/arc.go/tenancy"
)

func TestTenantSentinelsAndCodecs(t *testing.T) {
	for _, text := range []string{"", "[NotSet]", "Default", "Acme Group", "database/anything", "租户"} {
		id, err := tenancy.ParseID(text)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var decoded tenancy.ID
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != id {
			t.Fatal(decoded, err)
		}
		wire, err := id.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		if err := decoded.UnmarshalText(wire); err != nil || decoded != id {
			t.Fatal(err)
		}
	}
	var zero tenancy.ID
	if zero.String() != "[NotSet]" || zero.IsSet() || !zero.IsDefault() || !tenancy.Default().IsDefault() || !tenancy.Default().IsSet() || zero == tenancy.Default() {
		t.Fatal("sentinels")
	}
	for _, text := range []string{" leading", "trailing ", "x\ny", "x\x00y", "\u0085", "\xff"} {
		if _, err := tenancy.ParseID(text); !errors.Is(err, tenancy.ErrInvalidID) {
			t.Fatal(text, err)
		}
	}
	for _, data := range []string{"null", "42", "{}", "[]", `" bad"`, `"bad\u0000"`, "{"} {
		id := tenancy.Default()
		if err := id.UnmarshalJSON([]byte(data)); !errors.Is(err, tenancy.ErrInvalidID) || id != tenancy.Default() {
			t.Fatal(data, id, err)
		}
	}
}

func TestTenantContexts(t *testing.T) {
	root := context.Background()
	if _, ok := tenancy.TenantFrom(root); ok {
		t.Fatal("absent")
	}
	parent := tenancy.WithTenant(root, tenancy.Default())
	child := tenancy.WithTenant(parent, tenancy.ID{})
	if id, ok := tenancy.TenantFrom(child); !ok || id.IsSet() {
		t.Fatal("shadow")
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			id, err := tenancy.ParseID(fmt.Sprint(i))
			if err != nil {
				t.Error(err)
				return
			}
			ctx := tenancy.WithTenant(parent, id)
			got, ok := tenancy.TenantFrom(ctx)
			if !ok || got != id {
				t.Error("isolation")
			}
		})
	}
	wg.Wait()
	if id, _ := tenancy.TenantFrom(parent); id != tenancy.Default() {
		t.Fatal("parent changed")
	}
}

func ExampleWithTenant() {
	id, err := tenancy.ParseID("Acme")
	if err != nil {
		panic(err)
	}
	ctx := tenancy.WithTenant(context.Background(), id)
	got, ok := tenancy.TenantFrom(ctx)
	fmt.Println(got, ok)
	// Output: Acme true
}

func FuzzTenantText(f *testing.F) {
	for _, seed := range []string{"", "[NotSet]", "Default", "Acme Group", "\x00", " leading", "租户"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		id, err := tenancy.ParseID(text)
		if err != nil {
			return
		}
		wire, err := id.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		var got tenancy.ID
		if err := got.UnmarshalText(wire); err != nil || got != id {
			t.Fatal("round trip", err)
		}
	})
}
func FuzzTenantJSON(f *testing.F) {
	for _, seed := range []string{`"Acme"`, "null", `"[NotSet]"`, `" bad"`, "{}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		id := tenancy.Default()
		err := id.UnmarshalJSON([]byte(text))
		if err != nil {
			if id != tenancy.Default() {
				t.Fatal("mutated on error")
			}
			return
		}
		wire, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var got tenancy.ID
		if err := json.Unmarshal(wire, &got); err != nil || got != id {
			t.Fatal("round trip", err)
		}
	})
}
