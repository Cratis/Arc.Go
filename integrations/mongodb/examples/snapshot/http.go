// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package snapshot

import (
	"context"
	"errors"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// SnapshotHTTPExample registers GET /authors on an Arc application without I/O.
// The caller supplies credential verification, tenant selection and membership;
// it must Start/Run the application and drain it before disconnecting the borrowed
// client. It neither seeds data nor trusts identity headers by itself.
func SnapshotHTTPExample(client *mongo.Client, authenticationHandlers []authentication.Handler, resolver tenancy.Resolver, membership tenancy.Membership) (*arc.Application, error) {
	if len(authenticationHandlers) == 0 || resolver == nil || membership == nil {
		return nil, errors.New("authentication, tenant selection and membership are required")
	}
	authors, err := mongodb.NewCollection[Author](client, mongodb.CollectionOptions{Database: "Library", Name: "Authors", SortFields: []queries.SortField{"id", "name"}, Ownership: mongodb.ApplicationOwned})
	if err != nil {
		return nil, err
	}
	builder, err := arc.NewBuilder(arc.Options{
		Authentication: authenticationHandlers, TenantResolver: resolver, RequireTenant: true, Membership: membership,
		OpenResources: func(context.Context) (execution.Resources, error) { return &authorResources{authors: authors}, nil },
	})
	if err != nil {
		return nil, err
	}
	if err := registerAuthors(builder.Queries(), authors); err != nil {
		return nil, err
	}
	return builder.Build()
}
