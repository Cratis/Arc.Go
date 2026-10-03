// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package snapshot_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func ExampleSnapshotExample() {
	// This example's scope is complete registration, not client/HTTP setup.
	// The external boundary test also executes it through the real pipeline
	// and driver with recorded no-database wire responses.
	pipeline, err := snapshot.SnapshotExample(&mongo.Client{}, tenancy.MembershipFunc(func(_ context.Context, p identity.Principal, _ tenancy.ID) (bool, error) {
		return p.IsAuthenticated(), nil
	}))
	if err != nil {
		panic(err)
	}
	registration, ok := pipeline.Lookup("Author.AllActive")
	fmt.Println(ok, registration.ReadModelType().Name())
	// Output: true Author
}
