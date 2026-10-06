// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"context"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
)

//arc:command name=Add path=/add
//arc:allow-anonymous
type add struct {
	Name string `json:"name"`
}

func (c add) Provide(context.Context) (string, error) { return c.Name, nil }
func (add) Handle(ctx context.Context, name string, write writer) (int, error) {
	return write(ctx, name)
}
func (c add) Validate(context.Context) ([]validation.Result, error) {
	if c.Name == "" {
		return []validation.Result{{Severity: validation.Error, Message: "Name required"}}, nil
	}
	return nil, nil
}

type writer func(context.Context, string) (int, error)

//arc:command
//arc:allow-anonymous
type bindings struct {
	Code int `json:"code"`
}

func (c bindings) Handle() (bindings, error) { return c, nil }

//arc:ignore
func (bindings) Provide() (int, error) { panic("ignored preparation must not run") }

//arc:command
//arc:allow-anonymous
type singular struct{}

func (singular) Provide() (validation.Result, error) {
	return validation.Result{Severity: validation.Error, Message: "Rejected"}, nil
}
func (singular) Handle(writer) error { panic("must not run") }

//arc:command
//arc:allow-anonymous
type resultControl struct{}

func (resultControl) Provide(info commands.CommandContext) (commands.Result[commands.NoResponse], error) {
	return commands.Unauthorized(info.CorrelationID(), "Denied"), nil
}
func (resultControl) Handle(writer) error { panic("must not run") }

//arc:readmodel name=Item
//arc:authorize roles=Reader
type item struct {
	Name string `json:"name"`
}

func (item) All() ([]item, error) { return []item{{Name: "all"}}, nil }

//arc:query name=Private
//arc:allow-anonymous
func (_ item) private(ctx context.Context, args queries.NoArguments) (*item, error) {
	return &item{Name: "private"}, nil
}

//arc:query model=item name=Recent
//arc:allow-anonymous
func recent() (queries.Page[item], error) {
	return queries.Page[item]{Items: []item{{Name: "recent"}}, TotalItems: 1}, nil
}

func (item) Array() ([1]*item, error)   { return [1]*item{{Name: "array"}}, nil }
func (item) Unrelated() (string, error) { return "ordinary", nil }
func (i item) Stateful() (item, error)  { return i, nil }

//arc:ignore
func (item) Ignored() (item, error) { return item{}, nil }
