// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization_test

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type wireNotice interface{ notice() }
type wireUrgent struct {
	Title    string
	Priority int
}

func (wireUrgent) notice() {}

type wireOther struct{ Title string }

func (wireOther) notice() {}

type wireEnvelope struct{ Notice wireNotice }

const urgentID = "b7807b28-201c-4c65-933f-110706495ea3"

func TestDeclaredDerivedRoundTripAndFailureAtomicity(t *testing.T) {
	declaration := serialization.DerivedDeclaration{ID: urgentID, Base: reflect.TypeFor[wireNotice](), Type: reflect.TypeFor[wireUrgent]()}
	if err := serialization.RegisterDerivedTypes(declaration); err != nil {
		t.Fatal(err)
	}
	if err := serialization.RegisterDerivedTypes(declaration); err != nil {
		t.Fatal("idempotent declaration", err)
	}
	body, err := serialization.Marshal(wireEnvelope{Notice: wireUrgent{Title: "urgent", Priority: 7}})
	if err != nil || !bytes.Contains(body, []byte(`"_derivedTypeId":"`+urgentID+`"`)) {
		t.Fatal(string(body), err)
	}
	var result wireEnvelope
	if err := serialization.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if got, ok := result.Notice.(wireUrgent); !ok || got.Priority != 7 || got.Title != "urgent" {
		t.Fatal(result.Notice)
	}
	for _, payload := range []string{
		`{"notice":{"title":"missing"}}`,
		`{"notice":{"_derivedTypeId":"unknown","title":"wrong"}}`,
		`{"notice":{"_derivedTypeId":"` + urgentID + `","_derivedTypeId":"` + urgentID + `"}}`,
	} {
		before := result
		if err := serialization.Unmarshal([]byte(payload), &result); err == nil {
			t.Fatal("accepted", payload)
		}
		if !reflect.DeepEqual(result, before) {
			t.Fatal("failed bind mutated target")
		}
	}
	if _, err := serialization.Marshal(wireEnvelope{Notice: wireOther{Title: "undeclared"}}); err == nil {
		t.Fatal("undeclared implementation encoded")
	}
	if err := serialization.RegisterDerivedTypes(serialization.DerivedDeclaration{ID: urgentID, Base: reflect.TypeFor[wireNotice](), Type: reflect.TypeFor[wireOther]()}); err == nil {
		t.Fatal("ID conflict accepted")
	}
	var duplicate *serialization.DuplicateMemberError
	if err := serialization.Unmarshal([]byte(`{"notice":{"_derivedTypeId":"`+urgentID+`","_derivedTypeId":"`+urgentID+`"}}`), &result); !errors.As(err, &duplicate) {
		t.Fatal(err)
	}
}

func TestDerivedRegistryConcurrentReadsAndIdempotentRegistration(t *testing.T) {
	declaration := serialization.DerivedDeclaration{ID: urgentID, Base: reflect.TypeFor[wireNotice](), Type: reflect.TypeFor[wireUrgent]()}
	if err := serialization.RegisterDerivedTypes(declaration); err != nil {
		t.Fatal(err)
	}
	var joined sync.WaitGroup
	for range 8 {
		joined.Go(func() {
			if err := serialization.RegisterDerivedTypes(declaration); err != nil {
				t.Error(err)
			}
			body, err := serialization.Marshal(wireEnvelope{Notice: wireUrgent{}})
			if err != nil {
				t.Error(err)
				return
			}
			var value wireEnvelope
			if err := serialization.Unmarshal(body, &value); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
}
