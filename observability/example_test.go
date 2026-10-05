// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observability_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/observability"
)

type announce struct{ Message string }

func ExampleRecorder() {
	recorder, err := observability.NewRecorder(observability.Options{})
	if err != nil {
		panic(err)
	}
	var registry commands.Registry
	if err := commands.Register(&registry, commands.Void(func(announce, context.Context) error { return nil })); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{Diagnostics: recorder})
	if err != nil {
		panic(err)
	}
	result, err := pipeline.Execute(context.Background(), announce{Message: "not in diagnostics"})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.IsSuccess())
	// Export explicitly, after execution. No callback runs inside the pipeline.
	report := recorder.Drain(64, func(event observability.Observation) error {
		fmt.Println(event.Outcome.String())
		return nil
	})
	fmt.Println(report.Removed)
	// Output:
	// true
	// success
	// 1
}
