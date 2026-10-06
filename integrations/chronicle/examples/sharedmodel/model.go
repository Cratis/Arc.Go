// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package sharedmodel demonstrates one projection target and Arc query model.
// Registration is explicit and independent; construction does not contact a kernel.
package sharedmodel

import (
	"context"
	"encoding/json"
	"fmt"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/projections"
	"github.com/cratis/chronicle.go/readmodels"
	"github.com/cratis/fundamentals.go/concepts"
)

// ProductName is a string concept with the same text and JSON representation.
type ProductName string

var _ concepts.Concept[string] = ProductName("")

// ConceptValue returns the underlying scalar.
func (n ProductName) ConceptValue() string { return string(n) }

// MarshalText returns the name's text representation.
func (n ProductName) MarshalText() ([]byte, error) { return []byte(n), nil }

// UnmarshalText accepts a name's text representation.
func (n *ProductName) UnmarshalText(data []byte) error { *n = ProductName(data); return nil }

// MarshalJSON returns the name as a JSON string.
func (n ProductName) MarshalJSON() ([]byte, error) { return json.Marshal(string(n)) }

// UnmarshalJSON decodes a JSON string without changing the name on failure.
func (n *ProductName) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*n = ProductName(value)
	return nil
}

// ProductRegistered records the initial product state.
type ProductRegistered struct {
	DisplayName ProductName `json:"displayName"`
	URLValue    string      `json:"URL_value"`
	Note        string      `json:"note"`
}

// NoteCleared records removal of a product's optional note.
type NoteCleared struct{}

// Inventory is both Chronicle's projection target and Arc's read model.
// Only fields are persisted; its unnamed receiver is a query namespace.
//
//arc:readmodel
type Inventory struct {
	ID          string      `json:"id" arc:"identity" chronicle:"key" example:"shared"`
	ProductName ProductName `json:"product_name" chronicle:"set(@registered,from=displayName)"`
	URLValue    string      `json:"URL_value"`
	Note        *string     `json:"note" chronicle:"set(@registered);clear(@cleared)"`
	State       string      `json:"state" chronicle:"value(@registered,value=\"available\")"`
	Transient   string      `json:"-"`
}

// ByID supplies an Arc query parameter, independently of Chronicle's key metadata.
type ByID struct {
	ID string `json:"id"`
}

// Reader is the typed one-instance read boundary used by the namespace method.
// A Chronicle readmodels.Reader[Inventory] satisfies it directly.
type Reader interface {
	Get(context.Context, readmodels.Key) (readmodels.Instance[Inventory], error)
}

// ByID reads projected state; a missing instance is not a zero-valued model.
func (Inventory) ByID(ctx context.Context, args ByID, reader Reader) (Inventory, error) {
	instance, err := reader.Get(ctx, readmodels.Key(args.ID))
	if err != nil {
		return Inventory{}, err
	}
	if !instance.Exists {
		return Inventory{}, fmt.Errorf("inventory does not exist")
	}
	return instance.Value, nil
}

// RegisterChronicle registers events, the model and model-bound projection only.
// It does not register Arc queries or install a shared global registry.
func RegisterChronicle(registry *chronicle.Registry) (readmodels.Model[Inventory], error) {
	registered, err := chronicle.RegisterEvent[ProductRegistered](registry)
	if err != nil {
		return readmodels.Model[Inventory]{}, err
	}
	cleared, err := chronicle.RegisterEvent[NoteCleared](registry)
	if err != nil {
		return readmodels.Model[Inventory]{}, err
	}
	model, err := chronicle.RegisterReadModel[Inventory](registry)
	if err != nil {
		return model, err
	}
	return model, registry.AddProjection(projections.ModelBound(model,
		projections.BindEvent("registered", registered), projections.BindEvent("cleared", cleared),
		projections.FromEvent(registered)))
}

// RegisterArc registers the same type and adapts its namespace method to HTTP.
// readerFor selects the request's store/tenant and must support concurrent calls.
func RegisterArc(builder *arc.Builder, readerFor func(context.Context) (Reader, error)) error {
	if err := queries.RegisterReadModel[Inventory](builder); err != nil {
		return err
	}
	return queries.Register[Inventory](builder, "ByID", queries.Function(func(ctx context.Context, args ByID) (Inventory, error) {
		reader, err := readerFor(ctx)
		if err != nil {
			return Inventory{}, err
		}
		return Inventory{}.ByID(ctx, args, reader)
	}), queries.WithPath[ByID]("/inventory/by-id"))
}
