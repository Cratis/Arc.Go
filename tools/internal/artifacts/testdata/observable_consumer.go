// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

//arc:namespace Shop.Tasks
package consumer

import (
	"context"

	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

//arc:readmodel
//arc:allow-anonymous
type Task struct {
	ID    string `json:"id" arc:"identity"`
	Title string `json:"title" sortable:"true"`
}

type WatchArgs struct {
	Board string `json:"board" query:"required"`
}
type TaskFeed interface {
	ForBoard(context.Context, string) (observable.Source[[]Task], error)
}

func (Task) Watch(ctx context.Context, args WatchArgs, feed TaskFeed) (observable.Source[[]Task], error) {
	return feed.ForBoard(ctx, args.Board)
}

//arc:authorize roles=Reader
func (Task) Private(ctx context.Context, args WatchArgs, feed TaskFeed) (observable.Source[[]Task], error) {
	return feed.ForBoard(ctx, args.Board)
}

func (Task) All() ([]Task, error) { return nil, nil }
func (Task) Single() (observable.Source[Task], error) {
	return observable.NewState(Task{}, observable.SubjectOptions[Task]{})
}
func (Task) Nullable() (*observable.State[*Task], error) {
	return observable.NewState[*Task](nil, observable.SubjectOptions[*Task]{})
}
func (Task) Current() (observable.CurrentSource[Task], error) {
	return observable.NewState(Task{}, observable.SubjectOptions[Task]{})
}
func (Task) Subject() (*observable.Subject[Task], error) {
	return observable.NewSubject(observable.SubjectOptions[Task]{})
}
func (Task) Pending() (*observable.State[Task], error) {
	return observable.NewPendingState(observable.SubjectOptions[Task]{})
}
func (Task) Array() (observable.Source[[2]Task], error)                            { return nil, nil }
func (Task) Page() (observable.Source[queries.Page[Task]], error)                  { return nil, nil }
func (Task) Changes() (observable.Source[queries.ObservedCollection[Task]], error) { return nil, nil }

type TaskSource = observable.Source[[]Task]

//arc:query model=Task name=Alias
func WatchAlias() (TaskSource, error) { return nil, nil }
