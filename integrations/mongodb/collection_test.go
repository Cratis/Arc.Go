// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type author struct {
	ID   int32  `json:"id" bson:"_id"`
	Name string `json:"name" bson:"Name"`
}

func TestDatabaseNames(t *testing.T) {
	for _, tc := range []struct {
		base, tenant, want string
		valid              bool
	}{
		{"Library", "[NotSet]", "Library", true},
		{"Library", "Default", "Library", true},
		{"Library", "default", "Library+default", true},
		{"Library", "Tenant-A", "Library+Tenant-A", true},
		{"图书馆", "租户", "图书馆+租户", true},
		{"Library", "A+B", "Library+A+B", true},
		{"Library", "A B", "", false},
		{"", "Default", "", false},
		{strings.Repeat("a", 63), "Default", strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), "Default", "", false},
		{strings.Repeat("a", 62), "x", "", false},
	} {
		t.Run(tc.base+"/"+tc.tenant, func(t *testing.T) {
			tenant, err := tenancy.ParseID(tc.tenant)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DatabaseName(tc.base, tenant)
			if tc.valid && (err != nil || got != tc.want) {
				t.Fatalf("got %q error %v", got, err)
			}
			if !tc.valid && (!errors.Is(err, ErrConfiguration) || got != "") {
				t.Fatalf("got %q error %v", got, err)
			}
		})
	}
	for _, character := range "/\\. \"$*<>:|?\x00" {
		if _, err := DatabaseName("a"+string(character)+"b", tenancy.Default()); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("character %q accepted", character)
		}
	}
	if _, err := DatabaseName("bad\xff", tenancy.Default()); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
}

func TestBindingCopiesConfigurationAndBorrowsClient(t *testing.T) {
	// A zero driver client proves the constructor does not connect or initialize
	// driver workers. Applications must supply their properly configured client.
	client := &mongo.Client{}
	fields := []queries.SortField{"id", "name"}
	config := CollectionOptions{Database: "Library", Name: "Authors", SortFields: fields, Ownership: ChronicleOwned}
	binding, err := NewCollection[author](client, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "changed"
	config.Ownership = ApplicationOwned
	fields[0] = "changed"
	if binding.options.Database != "Library" || binding.options.Ownership != ChronicleOwned || binding.options.SortFields[0] != "id" {
		t.Fatal("retained caller configuration")
	}
	if binding.client != client || binding.sortNames["Name"] != "Name" || binding.sortNames["Id"] != "_id" {
		t.Fatal("invalid binding")
	}
	if _, exists := reflect.TypeOf(binding).MethodByName("Close"); exists {
		t.Fatal("binding acquired client ownership")
	}
	second, err := NewCollection[author](client, CollectionOptions{Database: "Library", Name: "Authors", Ownership: ApplicationOwned})
	if err != nil || second.registry == binding.registry {
		t.Fatalf("shared registry, error %v", err)
	}
	var workers sync.WaitGroup
	for _, name := range []string{"Tenant-A", "Tenant-B", "租户"} {
		workers.Go(func() {
			tenant, err := tenancy.ParseID(name)
			if err != nil {
				t.Error(err)
				return
			}
			handle, err := binding.forTenant(tenant)
			if err != nil {
				t.Error(err)
				return
			}
			if handle.Database().Name() != "Library+"+name || handle.Name() != "Authors" || handle.Database().Client() != client {
				t.Error("tenant/client coordinates changed")
			}
		})
	}
	workers.Wait()
}

func TestInvalidBindings(t *testing.T) {
	client := &mongo.Client{}
	for _, config := range []CollectionOptions{
		{}, {Database: "db", Name: "Authors"},
		{Database: "db", Name: "Authors", Ownership: 3},
		{Database: "db", Name: "system.authors", Ownership: ApplicationOwned},
		{Database: "db", Name: "Author$", Ownership: ApplicationOwned},
		{Database: "db", Name: "Author\x00", Ownership: ApplicationOwned},
		{Database: "db", Name: strings.Repeat("a", 253), Ownership: ApplicationOwned},
		{Database: "db", Name: "Authors", Ownership: ApplicationOwned, SortFields: []queries.SortField{"unknown"}},
		{Database: "db", Name: "Authors", Ownership: ApplicationOwned, SortFields: []queries.SortField{"name", "name"}},
	} {
		if _, err := NewCollection[author](client, config); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("configuration accepted, error %v", err)
		}
	}
	config := CollectionOptions{Database: "db", Name: "Authors", Ownership: ApplicationOwned}
	if _, err := NewCollection[author](nil, config); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
	type noID struct {
		V int32 `json:"v"`
	}
	type sliceID struct {
		ID []int32 `json:"id" bson:"_id"`
	}
	type pointerID struct {
		ID *int32 `json:"id" bson:"_id"`
	}
	if _, err := NewCollection[noID](client, config); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatal(err)
	}
	if _, err := NewCollection[sliceID](client, config); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatal(err)
	}
	if _, err := NewCollection[pointerID](client, config); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatal(err)
	}
	if _, err := NewCollection[*author](client, config); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatal(err)
	}
	type ambiguous struct {
		ID int32 `json:"id" bson:"_id"`
		A  int32 `json:"name"`
		B  int32 `json:"Name"`
	}
	config.SortFields = []queries.SortField{"name", "Name"}
	if _, err := NewCollection[ambiguous](client, config); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
	config.SortFields = nil
	for _, name := range []string{"Authors.v1", "system_authors", "图书 集合", "Authors-2026"} {
		config.Name = name
		if _, err := NewCollection[author](client, config); err != nil {
			t.Fatalf("valid collection rejected %q: %v", name, err)
		}
	}
	config.Name = strings.Repeat("a", 252) // 255-byte unsharded namespace
	binding, err := NewCollection[author](client, config)
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := tenancy.ParseID("x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.forTenant(tenant); !errors.Is(err, ErrConfiguration) {
		t.Fatal("resolved namespace overflow accepted")
	}
	var zero Collection[author]
	if _, err := zero.forTenant(tenancy.Default()); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
}

func FuzzNames(f *testing.F) {
	f.Add("Library", "Authors")
	f.Add("图书馆", "集合")
	f.Fuzz(func(t *testing.T, database, collection string) {
		name, err := DatabaseName(database, tenancy.Default())
		if err == nil && name != database {
			t.Fatal("name silently changed")
		}
		if validCollection(database, collection) && strings.ContainsAny(collection, "$\x00") {
			t.Fatal("invalid name accepted")
		}
	})
}
