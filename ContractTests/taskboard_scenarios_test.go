// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"testing"

	"github.com/cratis/arc.go/ContractTests/taskboard"
	"github.com/cratis/arc.go/arctest"
	"github.com/cratis/arc.go/queries"
)

func TestTaskBoardScenariosShareRealApplicationState(t *testing.T) {
	builder, err := taskboard.NewBuilder()
	if err != nil {
		t.Fatal(err)
	}
	scenario := arctest.New(t, builder)
	create := arctest.NewCommand[taskboard.CreateTask, taskboard.Created](scenario)
	validate, err := create.Validate(t.Context(), taskboard.CreateTask{Title: "Advisory only"})
	arctest.RequireNoResponse(t, validate, err)
	query := arctest.NewQuery[[]taskboard.Task](scenario, "Task.All")
	empty, err := query.Perform(t.Context(), queries.Request{})
	if got := arctest.RequireData(t, empty, err); len(got) != 0 {
		t.Fatal("validate mutated state")
	}
	result, err := create.Execute(t.Context(), taskboard.CreateTask{Title: "Ship scenarios"})
	created := arctest.RequireResponse(t, result, err)
	complete := arctest.NewCommand[taskboard.CompleteTask, taskboard.Task](scenario)
	completed, err := complete.Execute(t.Context(), taskboard.CompleteTask{TaskID: created.ID})
	arctest.RequireSuccess(t, completed, err)
	byID := arctest.NewQuery[*taskboard.Task](scenario, "Task.ByID")
	snapshot, err := byID.Perform(t.Context(), queries.RequestFor(taskboard.ByID{ID: created.ID}, queries.Parameters{}))
	got := arctest.RequireData(t, snapshot, err)
	if got == nil || got.ID != created.ID || got.Title != "Ship scenarios" || !got.Completed {
		t.Fatalf("task = %+v", got)
	}
}
