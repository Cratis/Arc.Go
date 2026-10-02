// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type boundInput struct {
	Name string `json:"name" validate:"required"`
}

func (c boundInput) Handle(context.Context) (row, error) { return row{"a1", c.Name}, nil }
func (c boundInput) Validate(context.Context) ([]validation.Result, error) {
	if c.Name == "blocked" {
		return []validation.Result{{Severity: validation.Error, Message: "Name is blocked", Members: []string{"name"}}}, nil
	}
	return nil, nil
}

// An unnamed namespace receiver is directly callable without a dummy handler.
func (row) All(context.Context, boundInput) ([]row, error) { return []row{}, nil }

func pipelineJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	pipelineMust(t, err)
	return canonical(t, data)
}

func assertPipelinePayloadAbsent(t *testing.T, data []byte, member string) {
	t.Helper()
	var body map[string]json.RawMessage
	pipelineMust(t, json.Unmarshal(data, &body))
	if _, exists := body[member]; exists {
		t.Fatalf("failed result retained %s: %s", member, data)
	}
	if _, exists := body["changeSet"]; exists {
		t.Fatalf("failed result retained changeSet: %s", data)
	}
}

func TestModelBoundPipelinesMatchExistingGoldens(t *testing.T) {
	var cr commands.Registry
	var qr queries.Registry
	pipelineMust(t, commands.Register(&cr, commands.Handle(boundInput.Handle)))
	pipelineMust(t, queries.Register[row](&qr, "All", queries.Function(row{}.All)))
	cp, err := cr.Build(commands.PipelineOptions{})
	pipelineMust(t, err)
	qp, err := qr.Build(queries.PipelineOptions{})
	pipelineMust(t, err)
	command, err := cp.Execute(pipelineContext(t), boundInput{Name: "Ada"})
	pipelineMust(t, err)
	query, err := qp.Perform(pipelineContext(t), "row.All", queries.RequestFor(boundInput{Name: "Ada"}, queries.Parameters{}))
	pipelineMust(t, err)
	data, err := os.ReadFile("fixtures/v1/envelopes.json")
	pipelineMust(t, err)
	var fixtures map[string]struct {
		Status int
		Body   json.RawMessage
	}
	pipelineMust(t, json.Unmarshal(data, &fixtures))
	for name, result := range map[string]interface {
		StatusCode() int
	}{"command-response": command, "query-success": query} {
		fixture, exists := fixtures[name]
		if !exists {
			t.Fatalf("missing golden %q", name)
		}
		if result.StatusCode() != fixture.Status || !bytes.Equal(pipelineJSON(t, result), canonical(t, fixture.Body)) {
			t.Fatalf("%s pipeline = %s, want = %s", name, pipelineJSON(t, result), fixture.Body)
		}
	}
}

type manifestCatalog struct{}

func (manifestCatalog) Contains(key di.Key) bool { return key == di.KeyFor[int]() }

func TestBothPipelineBuildsCheckSharedDependencyManifests(t *testing.T) {
	for _, kind := range []string{"policy", "validator"} {
		t.Run(kind, func(t *testing.T) {
			var cr commands.Registry
			var qr queries.Registry
			declaration := metadata.Authorization{}
			if kind == "policy" {
				declaration.Requirements = []metadata.AuthorizationRequirement{{Policy: "p"}}
			}
			pipelineMust(t, commands.Register[boundInput](&cr, commands.Handle(boundInput.Handle), commands.WithAuthorization[boundInput](declaration)))
			pipelineMust(t, queries.Register[row](&qr, "All", queries.Function(row{}.All), queries.WithAuthorization[boundInput](declaration)))
			var ar authorization.Registry
			if kind == "policy" {
				pipelineMust(t, authorization.RegisterPolicy(&ar, "p", func(context.Context, *execution.Scope) (authorization.PolicyFunc, error) {
					t.Fatal("Build constructed policy")
					return nil, nil
				}, authorization.PolicyOptions{}, di.KeyFor[int]()))
			}
			catalog := cr.Catalog()
			catalog.Queries = qr.Catalog().Queries
			evaluator, err := ar.Build(catalog, authorization.Options{})
			pipelineMust(t, err)
			var vr validation.Registry
			if kind == "validator" {
				pipelineMust(t, validation.RegisterScoped[boundInput](&vr, func(context.Context, *execution.Scope) (validation.Validator[boundInput], error) {
					t.Fatal("Build constructed validator")
					return nil, nil
				}, di.KeyFor[int]()))
			}
			graph, err := vr.Build()
			pipelineMust(t, err)
			want := authorization.ErrInvalidConfiguration
			if kind == "validator" {
				want = validation.ErrInvalidRegistration
			}
			if _, err := cr.Build(commands.PipelineOptions{Authorization: evaluator, Validation: graph}); !errors.Is(err, want) {
				t.Fatalf("command accepted missing %s manifest: %v", kind, err)
			}
			if _, err := qr.Build(queries.PipelineOptions{Authorization: evaluator, Validation: graph}); !errors.Is(err, want) {
				t.Fatalf("query accepted missing %s manifest: %v", kind, err)
			}
			_, err = cr.Build(commands.PipelineOptions{Authorization: evaluator, Validation: graph, DependencyCatalog: manifestCatalog{}})
			pipelineMust(t, err)
			_, err = qr.Build(queries.PipelineOptions{Authorization: evaluator, Validation: graph, DependencyCatalog: manifestCatalog{}})
			pipelineMust(t, err)
		})
	}
}

func TestSharedGraphAndModelConventionsBlockBothPipelines(t *testing.T) {
	var vr validation.Registry
	factories := 0
	pipelineMust(t, validation.RegisterScoped[boundInput](&vr, func(ctx context.Context, scope *execution.Scope) (validation.Validator[boundInput], error) {
		factories++
		if err := scope.CheckContext(ctx); err != nil {
			return nil, err
		}
		return validation.ValidatorFunc[boundInput](func(_ context.Context, input boundInput) ([]validation.Result, error) {
			if input.Name == "registered" {
				return []validation.Result{{Severity: validation.Error, Message: "Registered rule", Members: []string{"name"}}}, nil
			}
			return nil, nil
		}), nil
	}))
	graph, err := vr.Build()
	pipelineMust(t, err)
	var cr commands.Registry
	var qr queries.Registry
	handled, performed := 0, 0
	pipelineMust(t, commands.Register[boundInput](&cr, commands.Handle(func(c boundInput, ctx context.Context) (row, error) { handled++; return c.Handle(ctx) })))
	pipelineMust(t, queries.Register[row](&qr, "All", queries.Function(func(ctx context.Context, a boundInput) ([]row, error) { performed++; return (row{}).All(ctx, a) })))
	cp, err := cr.Build(commands.PipelineOptions{Validation: graph})
	pipelineMust(t, err)
	qp, err := qr.Build(queries.PipelineOptions{Validation: graph})
	pipelineMust(t, err)
	if factories != 0 {
		t.Fatal("Build activated graph validator")
	}
	for _, name := range []string{"", "blocked", "registered"} {
		ctx := pipelineContext(t)
		command, err := cp.Execute(ctx, boundInput{Name: name})
		pipelineMust(t, err)
		query, err := qp.Perform(ctx, "row.All", queries.RequestFor(boundInput{Name: name}, queries.Parameters{}))
		pipelineMust(t, err)
		if command.IsValid() || query.IsValid() || handled != 0 || performed != 0 || command.Details().CorrelationID != correlation.FromContext(ctx) || query.Details().CorrelationID != correlation.FromContext(ctx) {
			t.Fatalf("validation did not block %q: %#v / %#v", name, command.Details(), query.Details())
		}
		assertPipelinePayloadAbsent(t, pipelineJSON(t, command), "response")
		assertPipelinePayloadAbsent(t, pipelineJSON(t, query), "data")
	}
	validated, err := cp.Validate(pipelineContext(t), boundInput{Name: "Ada"})
	pipelineMust(t, err)
	if !validated.IsSuccess() || handled != 0 || factories != 7 {
		t.Fatalf("Validate = %#v, handler/factories = %d/%d", validated.Details(), handled, factories)
	}
}
