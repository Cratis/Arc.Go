// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"reflect"
	"slices"
	"unicode"

	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// Ownership declares the persistence owner, not ownership of the borrowed client.
type Ownership uint8

const (
	// ApplicationOwned declares ordinary application documents.
	ApplicationOwned Ownership = 1
	// ChronicleOwned requires complete raw-ciphertext release through a
	// renderer's application callback before typed publication.
	ChronicleOwned Ownership = 2
)

// CollectionOptions declares immutable collection coordinates and mapping.
// Zero ownership, empty names, duplicate/unknown sort fields, and ambiguous
// transport aliases fail construction. Names are explicit; no pluralization
// approximation is performed.
type CollectionOptions struct {
	// Database is the base database before tenant suffix resolution.
	Database string
	// Name is the exact case-preserving collection name.
	Name string
	// SortFields declares top-level JSON scalar names for query rendering.
	// The constructor copies this slice; BSON overrides remain storage names.
	SortFields []queries.SortField
	// Ownership must explicitly declare the document owner.
	Ownership Ownership
}

// Collection is a typed, immutable binding to an application-owned client.
// It has no Close method or promoted driver API.
// Construct it with NewCollection; the zero value is not a valid binding.
// Constructed bindings are safe for concurrent use without config mutation.
type Collection[T any] struct {
	client    *mongo.Client
	options   CollectionOptions
	registry  *bson.Registry
	sortNames map[queries.SortField]string
	id        field
}

// NewCollection borrows client and validates/copies options and T's storage
// graph without I/O, goroutines, or application codec execution. T must be a
// named struct with a declared nonnullable scalar BSON _id field. Each binding
// has its own registry, installed through the driver's public collection options;
// neither the client registry nor driver globals are changed.
func NewCollection[T any](client *mongo.Client, config CollectionOptions) (*Collection[T], error) {
	if client == nil || !validDatabase(config.Database) || !validCollection(config.Database, config.Name) ||
		(config.Ownership != ApplicationOwned && config.Ownership != ChronicleOwned) {
		return nil, ErrConfiguration
	}
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return nil, ErrUnsupportedModel
	}
	registry, codecs, err := newRegistry(t)
	if err != nil {
		return nil, err
	}
	model := codecs[t]
	if len(model.fields) == 0 {
		return nil, ErrUnsupportedModel
	}
	id := false
	var identity field
	fields := make(map[queries.SortField]*codec)
	storage := make(map[queries.SortField]string)
	for _, f := range model.fields {
		fields[queries.SortField(f.jsonName)] = f.codec
		storage[queries.SortField(f.jsonName)] = f.name
		if f.name == "_id" {
			id = f.codec.scalar()
			identity = f
		}
	}
	if !id {
		return nil, ErrUnsupportedModel
	}
	sortNames := make(map[queries.SortField]string)
	seen := make(map[queries.SortField]bool)
	for _, name := range config.SortFields {
		c := fields[name]
		if c == nil || seen[name] {
			return nil, ErrConfiguration
		}
		seen[name] = true
		for c.typeOf.Kind() == reflect.Pointer {
			c = c.element
		}
		if !c.scalar() {
			return nil, ErrConfiguration
		}
		alias := []rune(name)
		alias[0] = unicode.ToUpper(alias[0])
		for _, key := range []queries.SortField{name, queries.SortField(string(alias))} {
			if previous, exists := sortNames[key]; exists && previous != storage[name] {
				return nil, ErrConfiguration
			}
			sortNames[key] = storage[name]
		}
	}
	config.SortFields = slices.Clone(config.SortFields)
	return &Collection[T]{client: client, options: config, registry: registry, sortNames: sortNames, id: identity}, nil
}

// forTenant creates only a local driver handle, not a connection or operation.
// Renderers resolve one handle per admitted execution.
func (c *Collection[T]) forTenant(tenant tenancy.ID) (*mongo.Collection, error) {
	if c == nil || c.client == nil || c.registry == nil {
		return nil, ErrConfiguration
	}
	database, err := DatabaseName(c.options.Database, tenant)
	if err != nil || !validCollection(database, c.options.Name) {
		return nil, ErrConfiguration
	}
	return c.client.Database(database).Collection(c.options.Name,
		options.Collection().SetRegistry(c.registry).
			SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary())), nil
}
