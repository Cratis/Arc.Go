// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/identity"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/metadata"
)

type Changed struct {
	Name string `json:"name"`
}
type catalog struct{}

func (catalog) Descriptors() []c.EventDescriptor {
	return []c.EventDescriptor{{Type: reflect.TypeFor[Changed](), Identity: c.EventType{ID: "changed", Generation: 1}, Validate: func(v any) error { _, err := json.Marshal(v); return err }, Decode: func(body []byte) (any, error) {
		var value Changed
		err := json.Unmarshal(body, &value)
		return value, err
	}}}
}

type fakeFactory struct {
	begins, commits, rollbacks int
	entries                    []c.Entry
	scopes                     []c.LabeledScope
	result                     c.CommitResult
	err                        error
	metadata                   []c.Metadata
}

func (f *fakeFactory) Begin(context.Context, c.Coordinates) (c.Participant, c.CompletionOwner, error) {
	f.begins++
	return f, f, nil
}
func (f *fakeFactory) Stage(ctx context.Context, b c.Batch) error {
	f.entries = append(f.entries, b.Entries...)
	f.scopes = append(f.scopes, b.Scopes...)
	f.metadata = append(f.metadata, c.MetadataFrom(ctx))
	return nil
}
func (f *fakeFactory) Commit(context.Context) (c.CommitResult, error) {
	f.commits++
	return f.result, f.err
}
func (f *fakeFactory) Rollback() error { f.rollbacks++; f.entries = nil; return nil }
func setup(t *testing.T, f *fakeFactory) (*arc.Builder, *c.Integration) {
	t.Helper()
	builder, err := arc.NewBuilder(arc.Options{Namespace: "Tests"})
	must(t, err)
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: f, Events: catalog{}})
	must(t, err)
	return builder, integration
}
func start(t *testing.T, b *arc.Builder) *arc.Application {
	t.Helper()
	app, err := b.Build()
	must(t, err)
	must(t, app.Start(t.Context()))
	t.Cleanup(func() { must(t, app.Shutdown(context.Background())) })
	return app
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type Change struct {
	ID c.EventSourceID `json:"id"`
}
type Child struct {
	ID c.EventSourceID `json:"id"`
}

func TestReturnedEventsNestedOrderingAndRetargeting(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, integration := setup(t, f)
	must(t, integration.Install(builder))
	must(t, commands.Register[Child](builder, commands.Handle(func(Child, context.Context) (Changed, error) { return Changed{Name: "nested"}, nil }), commands.WithNoResponse[Child]()))
	must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.Outcome[c.EventSourceID], error) {
		result, err := inv.Pipeline().Execute(ctx, Child{ID: "nested-source"})
		if err != nil || !result.IsSuccess() {
			return commands.Outcome[c.EventSourceID]{}, err
		}
		return commands.Respond(c.EventSourceID("A"), c.Events(Changed{Name: "A1"}, c.EventForSource("B", Changed{Name: "B1"}), Changed{Name: "A2"})), nil
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "old"})
	must(t, err)
	if !result.IsSuccess() || f.begins != 1 || f.commits != 1 || len(f.entries) != 4 {
		t.Fatal(result.Details(), f)
	}
	for index, want := range []c.EventSourceID{"nested-source", "A", "B", "A"} {
		if f.entries[index].Source != want {
			t.Fatal(index, f.entries)
		}
	}
	if len(f.metadata[0].Causes) != 2 || f.metadata[0].Causes[0].Type != "Command" {
		t.Fatal(f.metadata)
	}
}
func TestValidationAndAuthorizationDoNotOpenStore(t *testing.T) {
	f := &fakeFactory{}
	builder, integration := setup(t, f)
	must(t, integration.Install(builder))
	must(t, commands.Register[Change](builder, commands.Handle(func(Change, context.Context) (Changed, error) { t.Fatal("denied handler"); return Changed{}, nil }), commands.WithAuthorization[Change](metadata.Authorization{}), commands.WithNoResponse[Change]()))
	app := start(t, builder)
	_, err := app.Commands().Validate(t.Context(), Change{ID: "a"})
	must(t, err)
	result, err := app.Commands().Execute(t.Context(), Change{ID: "a"})
	must(t, err)
	if result.IsSuccess() || f.begins != 0 || f.commits != 0 {
		t.Fatal(result, f)
	}
}
func TestFailedNestedCommandRollsBackAndUnknownCommitRetractsResponse(t *testing.T) {
	for _, nested := range []bool{true, false} {
		f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.OutcomeUnknown}}}
		builder, integration := setup(t, f)
		must(t, integration.Install(builder))
		cause := errors.New("child failed")
		must(t, commands.Register[Child](builder, commands.Void(func(Child, context.Context) error { return cause })))
		must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.Outcome[bool], error) {
			if nested {
				_, _ = inv.Pipeline().Execute(ctx, Child{ID: "a"})
			}
			return commands.Respond(false, Changed{Name: "event"}), nil
		})))
		result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
		if result.IsSuccess() {
			t.Fatal("failed command succeeded")
		}
		if _, present := result.Response(); present {
			t.Fatal("response retained")
		}
		if nested {
			if !errors.Is(err, cause) || f.commits != 0 || f.rollbacks != 1 {
				t.Fatal(err, f)
			}
		} else if !errors.Is(err, c.ErrUnknownOutcome) || f.commits != 1 {
			t.Fatal(err, f)
		}
	}
}

type PlainID struct {
	ID concepts.UUID `json:"id"`
}
type NullableID struct {
	ID *c.EventSourceID `json:"id" arc:"key"`
}
type PanickingID struct{}

func (PanickingID) GetEventSourceID() c.EventSourceID { panic("do not expose") }
func TestSourceIdentityIsSemanticAndDeclaredNullNeverGenerates(t *testing.T) {
	for _, which := range []string{"plain", "null", "panic"} {
		f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
		builder, integration := setup(t, f)
		must(t, integration.Install(builder))
		must(t, commands.Register[PlainID](builder, commands.Handle(func(PlainID, context.Context) (Changed, error) { return Changed{}, nil }), commands.WithNoResponse[PlainID]()))
		must(t, commands.Register[NullableID](builder, commands.Handle(func(NullableID, context.Context) (Changed, error) { return Changed{}, nil }), commands.WithNoResponse[NullableID]()))
		must(t, commands.Register[PanickingID](builder, commands.Handle(func(PanickingID, context.Context) (Changed, error) { return Changed{}, nil }), commands.WithNoResponse[PanickingID]()))
		var input any = PlainID{}
		if which == "null" {
			input = NullableID{}
		}
		if which == "panic" {
			input = PanickingID{}
		}
		result, err := start(t, builder).Commands().Execute(t.Context(), input)
		if which == "plain" {
			must(t, err)
			if len(f.entries) != 1 || len(f.entries[0].Source) != 36 {
				t.Fatal(f)
			}
		} else if result.IsSuccess() || !errors.Is(err, c.ErrInvalid) || f.begins != 0 {
			t.Fatal(result, err, f)
		}
	}
}
func TestTypedNilPreflightAndAnonymousActor(t *testing.T) {
	for _, nilEvent := range []bool{true, false} {
		f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
		builder, integration := setup(t, f)
		must(t, integration.Install(builder))
		must(t, commands.Register[Change](builder, commands.Handle(func(Change, context.Context) (commands.Outcome[commands.NoResponse], error) {
			if nilEvent {
				return commands.Effects[commands.NoResponse](Changed{}, (*Changed)(nil)), nil
			}
			return commands.Effects[commands.NoResponse](Changed{}), nil
		})))
		ctx := identity.WithPrincipal(t.Context(), identity.NewPrincipal(identity.PrincipalData{ID: "unverified", Name: "unverified"}))
		result, err := start(t, builder).Commands().Execute(ctx, Change{ID: "a"})
		if nilEvent {
			if result.IsSuccess() || !errors.Is(err, commands.ErrNilReturn) || f.begins != 0 {
				t.Fatal(result, err, f)
			}
		} else {
			must(t, err)
			if f.metadata[0].Actor != (c.Actor{}) {
				t.Fatal(f.metadata)
			}
		}
	}
}
