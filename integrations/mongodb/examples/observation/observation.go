// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package observation demonstrates manual Source[Find[T]] registration against
// the existing MongoDB renderer. It supplies no host, authentication or database.
package observation

import (
	"context"
	"errors"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Author is an application-owned read model; provision suitable row/sort indexes.
type Author struct {
	ID     int32  `json:"id" bson:"_id"`
	Name   string `json:"name" bson:"Name"`
	Active bool   `json:"active" bson:"Active"`
}

// ActiveAuthors returns trusted invalidation instructions, not rendered rows.
// Register this source against the renderer for the same collection binding.
func ActiveAuthors(watcher *mongodb.Watcher, authors *mongodb.Collection[Author]) (observable.Source[mongodb.Find[Author]], error) {
	return mongodb.Observe(watcher, authors, mongodb.Find[Author]{Filter: bson.D{{Key: "Active", Value: true}}})
}

type authorResources struct{ authors *mongodb.Collection[Author] }

func (*authorResources) Close(context.Context) error { return nil }

// ObservationExample constructs a complete manually registered pipeline and
// lazy watcher without I/O. lifetime is application-owned, never a request.
// Supply verified ingress identity/tenant and membership before Open or Perform.
// Query identity is Author.Active. Waiting/streaming renders the initial baseline;
// non-wait snapshots are pending. Drain CloseObservations, then Watcher.Close,
// then disconnect the borrowed client. Continue any timed-out join before disposal.
func ObservationExample(lifetime context.Context, client *mongo.Client, membership tenancy.Membership) (queries.ObservablePipeline, *mongodb.Watcher, error) {
	if membership == nil {
		return nil, nil, errors.New("tenant membership is required")
	}
	authors, err := mongodb.NewCollection[Author](client, mongodb.CollectionOptions{Database: "Library", Name: "Authors", SortFields: []queries.SortField{"id", "name"}, Ownership: mongodb.ApplicationOwned})
	if err != nil {
		return nil, nil, err
	}
	watcher, err := mongodb.NewWatcher(lifetime, client, mongodb.WatcherOptions{})
	if err != nil {
		return nil, nil, err
	}
	var registry queries.Registry
	err = queries.RegisterObservable[Author, queries.NoArguments, mongodb.Find[Author]](&registry, "Active", queries.Function(
		func(context.Context, queries.NoArguments) (observable.Source[mongodb.Find[Author]], error) {
			return ActiveAuthors(watcher, authors)
		}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{}),
		queries.WithRenderer[queries.NoArguments, mongodb.Find[Author], []Author](
			func(ctx context.Context, scope *execution.Scope) (queries.Renderer[mongodb.Find[Author], []Author], error) {
				// A fresh callback-scoped view arrives on every emission. Never
				// capture this scope or the performer context in a refetch closure.
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
			}))
	if err != nil {
		return nil, nil, err
	}
	pipeline, err := registry.Build(queries.PipelineOptions{RequireTenant: true, Membership: membership, OpenResources: func(context.Context) (execution.Resources, error) { return &authorResources{authors: authors}, nil }})
	if err != nil {
		return nil, nil, err
	}
	return pipeline.(queries.ObservablePipeline), watcher, nil
}
