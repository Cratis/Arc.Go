// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metadata_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

type declaredCommand struct {
	_    struct{} `json:"-" arc:"command,name=Add,namespace=Shop.Items,path=/items/add,block-on=warning,exclude-from-discovery" authorize:"roles=Editor|Admin,policy=CanWrite;roles=Approved"`
	ID   string   `json:"id" arc:"key"`
	Name string   `json:"name" validate:"required"`
}

type plainReadModel struct {
	ID string `json:"id" arc:"identity"`
}

func TestModelMetadataDefaultsAndDeclarationTags(t *testing.T) {
	model, err := metadata.InspectModel(reflect.TypeFor[declaredCommand](), "Default.Namespace")
	if err != nil {
		t.Fatal(err)
	}
	if model.Kind != metadata.CommandModel || model.Type.Identity() != "Shop.Items.Add" || model.Path != "/items/add" || !model.ExcludeFromDiscovery || model.KeyMember != "id" || model.BlockOnValidationSeverity == nil || *model.BlockOnValidationSeverity != validation.Warning {
		t.Fatalf("model = %+v", model)
	}
	if len(model.Authorization.Requirements) != 2 || !reflect.DeepEqual(model.Authorization.Requirements[0].Roles, []string{"Editor", "Admin"}) || model.Authorization.Requirements[0].Policy != "CanWrite" {
		t.Fatal(model.Authorization)
	}
	model.Authorization.Requirements[0].Roles[0] = "mutated"
	fresh, err := metadata.InspectModel(reflect.TypeFor[declaredCommand](), "Default.Namespace")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Authorization.Requirements[0].Roles[0] != "Editor" {
		t.Fatal("shared metadata")
	}
	plain, err := metadata.InspectModel(reflect.TypeFor[*plainReadModel](), "Shop")
	if err != nil || plain.Type.Identity() != "Shop.plainReadModel" || plain.IdentityMember != "id" || plain.Authorization != nil {
		t.Fatalf("plain = %+v %v", plain, err)
	}
}

func TestModelMetadataRejectsAmbiguityAndUnknownOptions(t *testing.T) {
	type duplicates struct {
		_ struct{} `json:"-" arc:"command"`
		_ struct{} `json:"-" arc:"command"`
	}
	type conflict struct {
		_ struct{} `json:"-" arc:"allow-anonymous" authorize:"roles=Admin"`
	}
	type typo struct {
		_ struct{} `json:"-" arc:"allow-anonymus"`
	}
	type scheme struct {
		_ struct{} `json:"-" authorize:"schemes=Bearer"`
	}
	type noJSON struct {
		_ struct{} `arc:"command"`
	}
	type keys struct {
		A string `arc:"key"`
		B string `arc:"key"`
	}
	type roles struct {
		_ struct{} `json:"-" authorize:"roles=Admin|"`
	}
	type validationTypo struct {
		Name string `validate:"requried"`
	}
	type kindConflict struct {
		_ struct{} `json:"-" arc:"command,readmodel"`
	}
	for _, typ := range []reflect.Type{nil, reflect.TypeFor[int](), reflect.TypeFor[struct{}](), reflect.TypeFor[duplicates](), reflect.TypeFor[conflict](), reflect.TypeFor[typo](), reflect.TypeFor[scheme](), reflect.TypeFor[noJSON](), reflect.TypeFor[keys](), reflect.TypeFor[roles](), reflect.TypeFor[validationTypo](), reflect.TypeFor[kindConflict]()} {
		if _, err := metadata.InspectModel(typ, ""); !errors.Is(err, metadata.ErrInvalidModel) {
			t.Fatalf("%v = %v", typ, err)
		}
	}
}

func TestExplicitAndAnonymousModelDeclarations(t *testing.T) {
	type anonymous struct {
		_ struct{} `json:"-" arc:"readmodel,allow-anonymous"`
	}
	type authenticated struct {
		_ struct{} `json:"-" authorize:""`
	}
	model, err := metadata.InspectModel(reflect.TypeFor[anonymous](), "")
	if err != nil || model.Authorization == nil || !model.Authorization.AllowAnonymous {
		t.Fatal(model, err)
	}
	model, err = metadata.InspectModel(reflect.TypeFor[authenticated](), "")
	if err != nil || model.Authorization == nil || model.Authorization.AllowAnonymous {
		t.Fatal(model, err)
	}
	model.Authorization = &metadata.Authorization{AllowAnonymous: true, Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Admin"}}}}
	if !errors.Is(model.Validate(), metadata.ErrInvalidModel) {
		t.Fatal("generated/manual declaration accepted conflict")
	}
}

func TestQueryTagEscapingAndInvalidCombinations(t *testing.T) {
	tags, err := metadata.ParseQueryTags(`default=a\,b\=c\\d`)
	if err != nil || !tags.HasDefault || tags.Default != `a,b=c\d` {
		t.Fatal(tags, err)
	}
	tags, err = metadata.ParseQueryTags("required,preservePresence")
	if err != nil || !tags.Required || !tags.PreservePresence {
		t.Fatal(tags, err)
	}
	tags, err = metadata.ParseQueryTags("preservePresence,default=1")
	if err != nil || !tags.HasDefault || !tags.PreservePresence {
		t.Fatal(tags, err)
	}
	for _, text := range []string{"required,default=1", "default", "required=false", "preservepresence", "default=1,default=2", `default=\z`, "required,"} {
		if _, err := metadata.ParseQueryTags(text); !errors.Is(err, metadata.ErrInvalidModel) {
			t.Fatalf("%q = %v", text, err)
		}
	}
}

func TestExplicitQueryMethodsRetainRouteAlgorithm(t *testing.T) {
	for _, method := range []metadata.QueryHTTPMethod{metadata.QueryHTTPDefault, metadata.QueryHTTPGet, metadata.QueryHTTPQuery} {
		catalog := metadata.Catalog{Version: metadata.Version, Queries: []metadata.Query{{ReadModel: metadata.TypeName{Name: "Item", Namespace: "Shop"}, Name: "All", HTTPMethod: method, ExcludeFromDiscovery: true}}}
		endpoints, err := metadata.Resolve(catalog, metadata.DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if method == metadata.QueryHTTPDefault {
			want = 2
		}
		if len(endpoints) != want || endpoints[0].Path != "/api/shop/all" {
			t.Fatal(endpoints)
		}
		if method != metadata.QueryHTTPDefault && endpoints[0].Method != string(method) {
			t.Fatal(endpoints)
		}
	}
	catalog := metadata.Catalog{Version: metadata.Version, Queries: []metadata.Query{{ReadModel: metadata.TypeName{Name: "Item"}, Name: "All", HTTPMethod: metadata.QueryHTTPQuery}}}
	if _, err := metadata.Resolve(catalog, metadata.Options{}); err == nil {
		t.Fatal("disabled QUERY silently accepted")
	}
}

func FuzzQueryTags(f *testing.F) {
	for _, seed := range []string{"", "required,preservePresence", `default=a\,b`, "default=", "required,default=1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		tags, err := metadata.ParseQueryTags(input)
		if err == nil && tags.HasDefault && tags.Required {
			t.Fatal("contradictory query tags")
		}
	})
}
