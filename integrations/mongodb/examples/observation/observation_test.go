// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observation_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb/examples/observation"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func ExampleObservationExample() {
	lifetime := context.Background()
	pipeline, watcher, err := observation.ObservationExample(lifetime, &mongo.Client{}, tenancy.MembershipFunc(func(context.Context, identity.Principal, tenancy.ID) (bool, error) { return true, nil }))
	if err != nil {
		panic(err)
	}
	query, ok := pipeline.Lookup("Author.Active")
	fmt.Println(ok, query.DataType())
	if err := pipeline.CloseObservations(lifetime); err != nil {
		panic(err)
	}
	if err := watcher.Close(lifetime); err != nil {
		panic(err)
	}
	// Output: true []observation.Author
}
