// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package structvalidation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/recipes/internal/fixture"
	"github.com/cratis/arc.go/recipes/structvalidation"
	"github.com/cratis/arc.go/validation"

	"github.com/go-playground/validator/v10"
)

const (
	accountPath  = "/api/accounts/open-account"
	accountsPath = "/api/accounts/by-email"
)

type Label struct {
	Value string `json:"value" playground:"min=2"`
}

// OpenAccount mixes Arc's portable required tag with go-playground rules.
type OpenAccount struct {
	Name   string  `json:"name" validate:"required" playground:"max=10"`
	Email  string  `json:"email" playground:"required,email"`
	Labels []Label `json:"labels" playground:"dive"`
}

type Account struct {
	Email string `json:"email"`
}

type ByEmail struct {
	Email string `json:"email" playground:"required,email"`
}

type finding struct {
	Members      []string `json:"members"`
	Reason       string   `json:"reason"`
	ReasonDetail string   `json:"reasonDetail"`
	Message      string   `json:"message"`
}

type envelope struct {
	IsValid           bool      `json:"isValid"`
	ValidationResults []finding `json:"validationResults"`
}

func setup(t *testing.T) (*atomic.Int32, *fixture.Application) {
	t.Helper()
	opened := &atomic.Int32{}
	f := fixture.New(t, arc.Options{}, func(builder *arc.Builder) error {
		// recipe:start validator-register
		rules := structvalidation.NewRules()
		err := commands.Register[OpenAccount](builder,
			commands.Handle(func(OpenAccount, context.Context) (string, error) {
				opened.Add(1)
				return "opened", nil
			}),
			commands.WithPath[OpenAccount](accountPath),
			commands.WithValidator(structvalidation.Validator[OpenAccount](rules)),
		)
		// recipe:end
		if err != nil {
			return err
		}
		return queries.Register[Account](builder, "ByEmail", queries.Function(func(_ context.Context, a ByEmail) ([]Account, error) {
			return []Account{{Email: a.Email}}, nil
		}), queries.WithPath[ByEmail](accountsPath), queries.WithValidator(structvalidation.Validator[ByEmail](rules)))
	})
	return opened, f
}

func decode(t *testing.T, r fixture.Response) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal([]byte(r.Body), &e); err != nil {
		t.Fatalf("%v: %q", err, r.Body)
	}
	return e
}

func membersOf(e envelope) [][]string {
	var members [][]string
	for _, f := range e.ValidationResults {
		members = append(members, f.Members)
	}
	return members
}

func TestRuleFailuresRejectBeforeTheHandler(t *testing.T) {
	opened, f := setup(t)
	server := fixture.Serve(t, f.App)
	body := `{"name":"Ada","email":"not-an-email","labels":[{"value":"ok"},{"value":"x"}]}`
	for _, path := range []string{accountPath + "/validate", accountPath} {
		r := fixture.Do(t, server, http.MethodPost, path, body, nil)
		e := decode(t, r)
		if r.Status != http.StatusBadRequest || e.IsValid {
			t.Fatalf("%s: got %d %q", path, r.Status, r.Body)
		}
		want := [][]string{{"email"}, {"labels[1].value"}}
		if got := membersOf(e); !slices.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("%s: members %q, want %q", path, got, want)
		}
		if e.ValidationResults[0].ReasonDetail != "email" || e.ValidationResults[1].ReasonDetail != "min=2" ||
			e.ValidationResults[0].Reason != "rule" || e.ValidationResults[0].Message != "email does not satisfy email." {
			t.Fatalf("%s: findings %+v", path, e.ValidationResults)
		}
	}
	if opened.Load() != 0 {
		t.Fatal("handler ran for an invalid command")
	}
}

func TestValidCommandsExecute(t *testing.T) {
	opened, f := setup(t)
	server := fixture.Serve(t, f.App)
	r := fixture.Do(t, server, http.MethodPost, accountPath, `{"name":"Ada","email":"ada@example.com","labels":[{"value":"ok"}]}`, nil)
	if r.Status != http.StatusOK || opened.Load() != 1 {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
}

func TestArcRequiredTagStillApplies(t *testing.T) {
	opened, f := setup(t)
	server := fixture.Serve(t, f.App)
	r := fixture.Do(t, server, http.MethodPost, accountPath, `{"email":"ada@example.com"}`, nil)
	if e := decode(t, r); r.Status != http.StatusBadRequest || !slices.ContainsFunc(membersOf(e), func(m []string) bool { return slices.Equal(m, []string{"name"}) }) {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
	if opened.Load() != 0 {
		t.Fatal("handler ran")
	}
}

func TestQueryArgumentsAreValidatedForGetAndQuery(t *testing.T) {
	_, f := setup(t)
	server := fixture.Serve(t, f.App)
	for _, r := range []fixture.Response{
		fixture.Do(t, server, http.MethodGet, accountsPath+"?email=nope", "", nil),
		fixture.Do(t, server, "QUERY", accountsPath, `{"arguments":{"email":"nope"}}`, nil),
	} {
		if e := decode(t, r); r.Status != http.StatusBadRequest || !slices.EqualFunc(membersOf(e), [][]string{{"email"}}, slices.Equal) {
			t.Fatalf("got %d %q", r.Status, r.Body)
		}
	}
	if r := fixture.Do(t, server, "QUERY", accountsPath, `{"arguments":{"email":"ada@example.com"}}`, nil); r.Status != http.StatusOK {
		t.Fatalf("valid: got %d %q", r.Status, r.Body)
	}
}

// RejectedTags uses go-playground's default tag name, which Arc owns.
type RejectedTags struct {
	Email string `json:"email" validate:"email"`
}

func TestPlaygroundRulesInArcsValidateTagFailRegistration(t *testing.T) {
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = commands.Register[RejectedTags](builder, commands.Handle(func(RejectedTags, context.Context) (string, error) { return "", nil }))
	if err == nil {
		_, err = builder.Build()
	}
	if err == nil {
		t.Fatal("expected Arc to reject the unknown validate rule")
	}
}

type PortableAndPlayground struct {
	Email string `json:"email" playground:"required,email" rules:"[{\"name\":\"maxLength\",\"arguments\":[30],\"message\":\"Email is too long.\"}]"`
}

func TestPlaygroundCoexistsWithPortableRules(t *testing.T) {
	portable, err := validation.NewPortable[PortableAndPlayground]()
	if err != nil {
		t.Fatal(err)
	}
	playground := structvalidation.Validator[PortableAndPlayground](structvalidation.NewRules())
	for _, tc := range []struct {
		value      string
		portable   int
		playground int
	}{
		{"ada@example.com", 0, 0},
		{"not-an-email", 0, 1},
		{"a-very-long-email-address@example.com", 1, 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			model := PortableAndPlayground{Email: tc.value}
			for _, check := range []struct {
				validator validation.Validator[PortableAndPlayground]
				want      int
			}{{portable, tc.portable}, {playground, tc.playground}} {
				results, err := check.validator.Validate(t.Context(), model)
				if err != nil || len(results) != check.want {
					t.Fatalf("results = %+v, %v; want %d", results, err, check.want)
				}
			}
		})
	}
}

type Inner struct {
	Code string `playground:"required"`
}

type WireCommand struct {
	Inner
	DisplayName string `playground:"required"`
	Email       string `json:",omitempty" playground:"required,email"`
}

func TestHTTPFindingsUseCamelCaseAndFlattenedWireMembers(t *testing.T) {
	var handled atomic.Int32
	f := fixture.New(t, arc.Options{}, func(builder *arc.Builder) error {
		return commands.Register[WireCommand](builder,
			commands.Handle(func(WireCommand, context.Context) (string, error) {
				handled.Add(1)
				return "opened", nil
			}),
			commands.WithPath[WireCommand](accountPath),
			commands.WithValidator(structvalidation.Validator[WireCommand](structvalidation.NewRules())))
	})
	server := fixture.Serve(t, f.App)
	for _, path := range []string{accountPath + "/validate", accountPath} {
		r := fixture.Do(t, server, http.MethodPost, path, `{}`, nil)
		want := [][]string{{"code"}, {"displayName"}, {"email"}}
		if r.Status != http.StatusBadRequest || !slices.EqualFunc(membersOf(decode(t, r)), want, slices.Equal) {
			t.Fatalf("%s: got %d %q", path, r.Status, r.Body)
		}
	}
	r := fixture.Do(t, server, http.MethodPost, accountPath,
		`{"code":"ok","displayName":"Ada","email":"ada@example.com"}`, nil)
	if r.Status != http.StatusOK || handled.Load() != 1 {
		t.Fatalf("valid wire members: got %d %q, handled %d", r.Status, r.Body, handled.Load())
	}
}

func TestMemberlessStructFailureTargetsTheModel(t *testing.T) {
	rules := structvalidation.NewRules()
	rules.RegisterStructValidation(func(level validator.StructLevel) {
		level.ReportError(level.Current().Interface(), "", "", "account", "")
	}, WireCommand{})
	results, err := structvalidation.Validator[WireCommand](rules).Validate(t.Context(), WireCommand{
		Inner: Inner{Code: "ok"}, DisplayName: "Ada", Email: "ada@example.com",
	})
	if err != nil || len(results) != 1 || len(results[0].Members) != 0 {
		t.Fatalf("results = %+v, %v; want one model-level finding", results, err)
	}
}

func TestNonStructTargetsAreServerFaults(t *testing.T) {
	results, err := structvalidation.Validator[string](structvalidation.NewRules()).Validate(context.Background(), "x")
	if err == nil || results != nil {
		t.Fatalf("got %v, %v", results, err)
	}
}
