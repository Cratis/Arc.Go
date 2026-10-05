// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/readmodels"
)

type refusalCommand struct{}

func TestReadDecisionPreservesProtectedRefusalIdentitiesBeforeAcquisition(t *testing.T) {
	for _, protected := range []bool{true, false} {
		profileName := "advisory"
		if protected {
			profileName = "protected"
		}
		t.Run(profileName, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				want error
			}{
				{"nil decisions", integration.ErrInvalid},
				{"zero decisions", integration.ErrInvalid},
				{"empty key", integration.ErrInvalid},
				{"zero model", integration.ErrInvalid},
				{"unregistered model", integration.ErrNotRegistered},
				{"foreign integration frame", commands.ErrNoContext},
				{"store mismatch", integration.ErrMismatch},
			} {
				t.Run(tc.name, func(t *testing.T) {
					model, err := readmodels.Define[decisionModel](readmodels.WithIdentifier("decision-model"))
					if err != nil {
						t.Fatal(err)
					}
					catalog, err := readmodels.NewCatalog(model.Descriptor())
					if err != nil {
						t.Fatal(err)
					}
					newIntegration := func() *integration.Integration {
						t.Helper()
						i, err := integration.New(integration.Options{
							StoreResolver: func(context.Context, commands.CommandContext) (integration.Coordinates, error) {
								return integration.Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}, nil
							},
							Transactions: decisionTransactions{}, Events: decisionTransactions{},
						})
						if err != nil {
							t.Fatal(err)
						}
						return i
					}
					builder, err := arc.NewBuilder(arc.Options{})
					if err != nil {
						t.Fatal(err)
					}
					i := newIntegration()
					if err := i.Install(builder); err != nil {
						t.Fatal(err)
					}
					d := &Decisions{provider: commands.NewDecisionProvider(), integration: i, adapter: &adapter{models: catalog, store: "store"}}
					if err := builder.Commands().AddDecisionProvider(d.provider); err != nil {
						t.Fatal(err)
					}
					key := readmodels.Key("a")
					switch tc.name {
					case "nil decisions":
						d = nil
					case "zero decisions":
						d = &Decisions{}
					case "empty key":
						key = ""
					case "zero model":
						model = readmodels.Model[decisionModel]{}
					case "unregistered model":
						model, err = readmodels.Define[decisionModel](readmodels.WithIdentifier("unregistered"))
						if err != nil {
							t.Fatal(err)
						}
					case "foreign integration frame":
						d.integration = newIntegration()
					case "store mismatch":
						d.adapter.store = "other"
					}
					profile := commands.WithUnprotectedDecisions[refusalCommand]()
					if protected {
						profile = commands.WithProtectedDecisions[refusalCommand]()
					}
					var refusal error
					handled := false
					err = commands.Register(builder.Commands(), profile,
						commands.WithoutModelValidation[refusalCommand](), commands.WithNoResponse[refusalCommand](),
						commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ refusalCommand) (commands.Preparation[*Decision[decisionModel]], error) {
							read, err := ReadDecision(ctx, inv, d, model, key)
							refusal = err
							if read != nil {
								t.Error("refusal returned a decision")
							}
							return commands.Preparation[*Decision[decisionModel]]{}, err
						}, func(context.Context, *commands.Invocation, refusalCommand, *Decision[decisionModel]) (commands.NoResponse, error) {
							handled = true
							return commands.NoResponse{}, nil
						}))
					if err != nil {
						t.Fatal(err)
					}
					app, err := builder.Build()
					if err != nil {
						t.Fatal(err)
					}
					if err := app.Start(t.Context()); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if err := app.Shutdown(ctx); err != nil {
							t.Error(err)
						}
					})
					result, err := app.Commands().Execute(t.Context(), refusalCommand{})
					if result.IsSuccess() || handled || !errors.Is(err, tc.want) || !errors.Is(refusal, tc.want) || errors.Is(refusal, commands.ErrDecisionRead) != protected {
						t.Fatalf("success=%v handled=%v error=%v refusal=%v; want cause=%v protected=%v", result.IsSuccess(), handled, err, refusal, tc.want, protected)
					}
				})
			}
		})
	}
}
