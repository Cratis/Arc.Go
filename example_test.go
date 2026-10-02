// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"fmt"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/metadata"
)

func Example() {
	command := metadata.Command{Type: metadata.TypeName{Namespace: "Tasks.Registration", Name: "RegisterTask"}}
	catalog := metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{command}}
	routes, err := metadata.Resolve(catalog, metadata.DefaultOptions())
	if err != nil {
		fmt.Println(err)
		return
	}
	id, err := concepts.ParseUUID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		fmt.Println(err)
		return
	}
	// This constructs an outcome; it does not invoke a command or host an endpoint.
	result := commands.WithResponse(id, struct {
		ID string `json:"id"`
	}{ID: "a1"})
	response, present := result.Response()
	fmt.Println(routes[0].Method, routes[0].Path)
	fmt.Println(result.StatusCode(), response.ID, present)
	// Output:
	// POST /api/tasks/registration/register-task
	// 200 a1 true
}
