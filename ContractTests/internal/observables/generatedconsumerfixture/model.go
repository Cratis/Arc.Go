// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

//arc:namespace Contracts.Items
package generatedconsumerfixture

import (
	"context"
	"time"

	"github.com/cratis/arc.go/observable"
)

// Item has the stock client's conventional collection identity.
//
//arc:readmodel
//arc:allow-anonymous
type Item struct {
	ID        string    `json:"id" arc:"identity"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	Rank      int       `json:"rank,omitempty"`
	Enabled   bool      `json:"enabled,omitempty"`
}

// Arguments selects one immutable fixture subject.
type Arguments struct {
	Group string `json:"group" query:"required"`
}

// ItemFeed selects a lazy source without retaining the invocation scope.
type ItemFeed interface {
	ForGroup(context.Context, string) (observable.Source[[]Item], error)
}

// All is the actual model-bound input to production arc-gen.
//
//arc:query path=/items
func (Item) All(ctx context.Context, args Arguments, feed ItemFeed) (observable.Source[[]Item], error) {
	return feed.ForGroup(ctx, args.Group)
}

// Private exercises admission before dependency resolution and source creation.
//
//arc:authorize roles=Reader
func (Item) Private(ctx context.Context, args Arguments, feed ItemFeed) (observable.Source[[]Item], error) {
	return feed.ForGroup(ctx, args.Group)
}
