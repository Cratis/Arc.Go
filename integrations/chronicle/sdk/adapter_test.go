// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/serialization"
)

// Explicit names are first so encoding/json's folded-name fallback takes the
// wrong field. The SDK plan reserves each exact declared name independently.
type namedEvent struct {
	ExternalID string `json:"NAME"`
	Name       string
}
type previousNamedEvent struct {
	ExternalID string `json:"LEGACYNAME"`
	LegacyName string
}

func decoderAdapter(t *testing.T) *adapter {
	t.Helper()
	registry := chronicle.NewRegistry()
	current, err := chronicle.RegisterEvent[namedEvent](registry, events.WithID("named"), events.WithGeneration(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chronicle.RegisterEventGeneration[previousNamedEvent](registry, current, 1); err != nil {
		t.Fatal(err)
	}
	client, err := chronicle.NewClient(chronicle.WithRegistry(registry), chronicle.WithNamingPolicy(serialization.LegacyGoCamelCase))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	catalog, _, err := client.Catalogs("store")
	if err != nil {
		t.Fatal(err)
	}
	return &adapter{events: catalog}
}

func TestReturnedDescriptorUsesFrozenSDKCodec(t *testing.T) {
	a := decoderAdapter(t)
	for _, descriptor := range a.Descriptors() {
		var want any = namedEvent{Name: "current", ExternalID: "external"}
		if descriptor.Identity.Generation == 1 {
			want = previousNamedEvent{LegacyName: "previous", ExternalID: "old-external"}
		}
		sdkDescriptor, ok := a.events.Lookup(want)
		if !ok {
			t.Fatal("fixture descriptor missing")
		}
		// A concrete pointer and a value must use the same registered event shape.
		pointer := reflect.New(reflect.TypeOf(want))
		pointer.Elem().Set(reflect.ValueOf(want))
		for _, value := range []any{want, pointer.Interface()} {
			if err := descriptor.Validate(value); err != nil {
				t.Fatal(err)
			}
			body, err := sdkDescriptor.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			raw := reflect.New(descriptor.Type)
			if err := json.Unmarshal(body, raw.Interface()); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(raw.Elem().Interface(), want) {
				t.Fatal("fixture no longer demonstrates raw JSON codec loss")
			}
			got, err := descriptor.Decode(body)
			if err != nil || !reflect.DeepEqual(got, want) || reflect.TypeOf(got) != descriptor.Type {
				t.Fatalf("generation %d decoded %+v, %v; want %+v value", descriptor.Identity.Generation, got, err, want)
			}
		}
	}
}

func TestReturnedDescriptorPreservesSDKDecodeFailure(t *testing.T) {
	a := decoderAdapter(t)
	for _, descriptor := range a.Descriptors() {
		for _, body := range []string{"null", "[]", `{"NAME":`, `{"NAME":42,"LEGACYNAME":42}`, "{} trailing"} {
			got, err := descriptor.Decode([]byte(body))
			if got != nil || !errors.Is(err, chronicle.ErrProtocol) {
				t.Fatalf("generation %d malformed %q = %+v, %v", descriptor.Identity.Generation, body, got, err)
			}
		}
	}
}

var _ integration.EventCatalog = (*adapter)(nil)
