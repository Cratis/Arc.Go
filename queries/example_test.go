// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

//arc:readmodel
type Author struct {
	Name string `json:"name"`
}
type AuthorArguments struct {
	Prefix string `json:"prefix"`
}

// All is a static-equivalent namespace method; the unnamed receiver has no state.
func (Author) All(context.Context, queries.NoArguments) ([]Author, error) {
	return []Author{{Name: "Ada"}}, nil
}

//arc:query model=Author name=Recent
func RecentAuthors(context.Context, queries.NoArguments) ([]Author, error) {
	return []Author{{Name: "Grace"}}, nil
}
func exampleMust(err error) {
	if err != nil {
		panic(err)
	}
}
func exampleBuild(r *queries.Registry) queries.Pipeline {
	p, err := r.Build(queries.PipelineOptions{})
	exampleMust(err)
	return p
}

func ExampleRegister_namespaceMethod() {
	var registry queries.Registry
	exampleMust(queries.Register[Author](&registry, "All", queries.Function(func(ctx context.Context, args queries.NoArguments) ([]Author, error) { return Author{}.All(ctx, args) }), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})))
	result, err := queries.Perform[[]Author](context.Background(), exampleBuild(&registry), "Author.All", queries.Request{})
	exampleMust(err)
	authors, _ := result.Data()
	fmt.Println(authors[0].Name)
	// Output: Ada
}
func ExampleRegister_directedFunction() {
	var registry queries.Registry
	exampleMust(queries.Register[Author](&registry, "Recent", queries.Function(RecentAuthors), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})))
	result, err := queries.Perform[[]Author](context.Background(), exampleBuild(&registry), "Author.Recent", queries.Request{})
	exampleMust(err)
	authors, _ := result.Data()
	fmt.Println(authors[0].Name)
	// Output: Grace
}

type authorRepository struct{ authors []Author }

func (r authorRepository) All(context.Context) ([]Author, error) { return r.authors, nil }
func ExampleFunction_closureDependency() {
	repository := authorRepository{authors: []Author{{Name: "Ada"}}}
	var registry queries.Registry
	exampleMust(queries.Register[Author](&registry, "All", queries.Function(func(ctx context.Context, _ queries.NoArguments) ([]Author, error) { return repository.All(ctx) }), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})))
	result, err := queries.Perform[[]Author](context.Background(), exampleBuild(&registry), "Author.All", queries.Request{})
	exampleMust(err)
	authors, _ := result.Data()
	fmt.Println(authors[0].Name)
	// Output: Ada
}
func ExampleWithParameters_page() {
	var registry queries.Registry
	exampleMust(queries.Register[Author](&registry, "Page", queries.WithParameters(func(_ context.Context, _ queries.NoArguments, p queries.Parameters) (queries.Page[Author], error) {
		all := []Author{{Name: "Ada"}, {Name: "Grace"}}
		start := min(int(p.Paging.Skip()), len(all))
		end := min(start+int(p.Paging.Size), len(all))
		return queries.Page[Author]{Items: all[start:end], TotalItems: int64(len(all))}, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})))
	request := queries.RequestFor(queries.NoArguments{}, queries.Parameters{Paging: queries.Paging{Page: 1, Size: 1, IsPaged: true}})
	result, err := queries.Perform[[]Author](context.Background(), exampleBuild(&registry), "Author.Page", request)
	exampleMust(err)
	authors, _ := result.Data()
	fmt.Println(authors[0].Name, result.Details().Paging.TotalItems)
	// Output: Grace 2
}

type authorSelection struct{ activeOnly bool }

func ExampleWithRenderer_provider() {
	var registry queries.Registry
	renderer := queries.RendererFunc[authorSelection, []Author](func(_ context.Context, selection authorSelection, _ queries.QueryContext) (queries.RendererResult[[]Author], error) {
		if selection.activeOnly {
			return queries.RendererResult[[]Author]{Data: []Author{{Name: "Ada"}}, TotalItems: 1}, nil
		}
		return queries.RendererResult[[]Author]{Data: []Author{}, TotalItems: 0}, nil
	})
	exampleMust(queries.Register[Author](&registry, "Active", queries.Function(func(context.Context, queries.NoArguments) (authorSelection, error) {
		return authorSelection{activeOnly: true}, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}), queries.WithRenderer[queries.NoArguments](func(context.Context, *execution.Scope) (queries.Renderer[authorSelection, []Author], error) {
		return renderer, nil
	})))
	result, err := queries.Perform[[]Author](context.Background(), exampleBuild(&registry), "Author.Active", queries.Request{})
	exampleMust(err)
	authors, _ := result.Data()
	fmt.Println(authors[0].Name)
	// Output: Ada
}
func ExampleRegisterReadModelInterceptor() {
	var registry queries.Registry
	exampleMust(queries.Register[Author](&registry, "All", queries.Function(RecentAuthors), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})))
	exampleMust(queries.RegisterReadModelInterceptor(&registry, "mask", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[Author], error) {
		return queries.InterceptorFunc[Author](func(_ context.Context, author Author) (Author, error) { author.Name = "redacted"; return author, nil }), nil
	}))
	result, err := queries.Perform[[]Author](context.Background(), exampleBuild(&registry), "Author.All", queries.Request{})
	exampleMust(err)
	authors, _ := result.Data()
	fmt.Println(authors[0].Name)
	// Output: redacted
}
