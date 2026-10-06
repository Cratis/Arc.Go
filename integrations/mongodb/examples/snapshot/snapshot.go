// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package snapshot demonstrates complete manual snapshot registration with a
// borrowed, application-configured client. It does not create a client/server,
// authenticate ingress, seed data or install indexes.
package snapshot

import (
	"context"
	"errors"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Author is an application-owned persisted read model. Supply an index covering
// OwnerID, Active, Name and _id for your actual count/sort workload.
type Author struct {
	ID     int32  `json:"id" bson:"_id"`
	Name   string `json:"name" bson:"Name"`
	Active bool   `json:"active" bson:"Active"`
}

// AllActive is a namespace query returning a trusted MongoDB predicate.
func (Author) AllActive(context.Context, queries.NoArguments) (mongodb.Find[Author], error) {
	return mongodb.Find[Author]{Filter: bson.D{{Key: "Active", Value: true}}}, nil
}

type authorResources struct{ authors *mongodb.Collection[Author] }

// Close disposes the cheap operation holder, never the borrowed client.
func (*authorResources) Close(context.Context) error { return nil }

// SnapshotExample builds a complete query pipeline without database I/O. Pass a
// configured driver client and trusted membership authority. Before calling
// Perform, trusted ingress must put a verified principal and selected tenant in
// context. Query identity is Author.AllActive. Disconnect the client only after
// all pipeline calls drain; construction does not take its ownership.
func SnapshotExample(client *mongo.Client, membership tenancy.Membership) (queries.Pipeline, error) {
	if membership == nil {
		return nil, errors.New("tenant membership is required")
	}
	authors, err := mongodb.NewCollection[Author](client, mongodb.CollectionOptions{Database: "Library", Name: "Authors", SortFields: []queries.SortField{"id", "name"}, Ownership: mongodb.ApplicationOwned})
	if err != nil {
		return nil, err
	}
	var registry queries.Registry
	if err := registerAuthors(&registry, authors); err != nil {
		return nil, err
	}
	return registry.Build(queries.PipelineOptions{RequireTenant: true, Membership: membership, OpenResources: func(context.Context) (execution.Resources, error) { return &authorResources{authors: authors}, nil }})
}

func registerAuthors(registry *queries.Registry, authors *mongodb.Collection[Author]) error {
	err := queries.RegisterRenderer[mongodb.Find[Author], []Author](registry, func(ctx context.Context, scope *execution.Scope) (queries.Renderer[mongodb.Find[Author], []Author], error) {
		resources, err := execution.ResourcesAs[*authorResources](ctx, scope)
		if err != nil {
			return nil, err
		}
		return mongodb.NewRenderer(resources.authors, mongodb.RendererOptions[Author]{RowFilter: func(_ context.Context, q queries.QueryContext) (bson.D, error) {
			if !q.Principal().IsAuthenticated() || q.Principal().ID() == "" {
				return nil, errors.New("verified subject required")
			}
			return bson.D{{Key: "OwnerID", Value: q.Principal().ID()}}, nil
		}})
	})
	if err != nil {
		return err
	}
	return queries.Register[Author](registry, "AllActive", queries.Function(Author{}.AllActive), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{}), queries.WithPath[queries.NoArguments]("/authors"))
}
