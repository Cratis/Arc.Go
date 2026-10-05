// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type ReserveSeat struct{ Seat string }

type Seat struct{ Taken bool }

// seatSource is a provider-neutral test source. Its evidence is a fresh pointer
// per acquisition; stale makes every later Check refuse it.
type seatSource struct {
	acquired, checked, enrolled atomic.Int32
	stale                       atomic.Bool
}

func (s *seatSource) Admit(context.Context) error { return nil }
func (s *seatSource) Acquire(context.Context) (any, error) {
	s.acquired.Add(1)
	return &Seat{}, nil
}
func (s *seatSource) Check(_ context.Context, value any) error {
	s.checked.Add(1)
	if _, ok := value.(*Seat); !ok || s.stale.Load() {
		return errors.New("stale seat evidence")
	}
	return nil
}
func (s *seatSource) Enroll(context.Context, any) error { s.enrolled.Add(1); return nil }

type emptyEvidence struct{}

func (emptyEvidence) DecisionReads() []*commands.DecisionRead { return nil }

func seatTarget(provider *commands.DecisionProvider, key string) commands.DecisionTarget {
	return commands.DecisionTarget{Provider: provider, Model: reflect.TypeFor[Seat](), Store: "store", Namespace: "default", Key: key}
}

func addOwner(t *testing.T, r *commands.Registry) {
	t.Helper()
	must(t, r.AddDeferredCommitParticipant("owner", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
		return terminalParticipant{
			begin: func(context.Context, *commands.Invocation) error { return nil },
			complete: func(context.Context, *commands.Invocation, commands.Result[any]) (commands.CompletionReport, error) {
				return commands.CompletionReport{Disposition: commands.NoPersistedWork}, nil
			},
		}, nil
	}))
}

// seatPipeline builds a protected (or otherwise profiled) command whose Provide
// supplies the payload returned by provide and whose Handle counts invocations.
func seatPipeline(t *testing.T, provider *commands.DecisionProvider, handled *atomic.Int32,
	provide func(context.Context, *commands.Invocation, ReserveSeat) (commands.DecisionEvidence, error), profile ...commands.Option[ReserveSeat]) commands.Pipeline {
	t.Helper()
	var r commands.Registry
	options := append([]commands.Option[ReserveSeat]{
		commands.WithoutModelValidation[ReserveSeat](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c ReserveSeat) (commands.Preparation[commands.DecisionEvidence], error) {
			evidence, err := provide(ctx, inv, c)
			if err != nil {
				return commands.Preparation[commands.DecisionEvidence]{}, err
			}
			return commands.Provided(evidence), nil
		}, func(context.Context, *commands.Invocation, ReserveSeat, commands.DecisionEvidence) (commands.NoResponse, error) {
			handled.Add(1)
			return commands.NoResponse{}, nil
		}),
	}, profile...)
	must(t, commands.Register(&r, options...))
	must(t, r.AddDecisionProvider(provider))
	addOwner(t, &r)
	return build(t, &r, commands.PipelineOptions{})
}

func TestProtectedDecisionProfileIsAdmittedAtRegistration(t *testing.T) {
	handler := commands.Void(func(ReserveSeat, context.Context) error { return nil })
	validator := validation.ValidatorFunc[ReserveSeat](func(context.Context, ReserveSeat) ([]validation.Result, error) { return nil, nil })
	refused := map[string][]commands.Option[ReserveSeat]{
		"model-validation": {handler, commands.WithProtectedDecisions[ReserveSeat]()},
		"validator": {handler, commands.WithoutModelValidation[ReserveSeat](), commands.WithProtectedDecisions[ReserveSeat](),
			commands.WithValidator[ReserveSeat](validator)},
		"scoped-validator": {handler, commands.WithoutModelValidation[ReserveSeat](), commands.WithProtectedDecisions[ReserveSeat](),
			commands.WithScopedValidator(func(context.Context, *execution.Scope) (validation.Validator[ReserveSeat], error) {
				return validator, nil
			})},
		"operations": {handler, commands.WithoutModelValidation[ReserveSeat](), commands.WithProtectedDecisions[ReserveSeat](),
			commands.WithOperations[ReserveSeat]()},
	}
	for name, options := range refused {
		t.Run(name, func(t *testing.T) {
			var r commands.Registry
			err := commands.Register(&r, options...)
			var registration *commands.RegistrationError
			if !errors.Is(err, commands.ErrDecisionProfile) || !errors.Is(err, commands.ErrInvalidRegistration) || !errors.As(err, &registration) {
				t.Fatalf("Register = %v", err)
			}
			if r.ContainsCommandType(reflect.TypeFor[ReserveSeat]()) {
				t.Fatal("refused registration was recorded")
			}
		})
	}
	t.Run("conflicting", func(t *testing.T) {
		var r commands.Registry
		err := commands.Register(&r, handler, commands.WithoutModelValidation[ReserveSeat](),
			commands.WithProtectedDecisions[ReserveSeat](), commands.WithUnprotectedDecisions[ReserveSeat]())
		if !errors.Is(err, commands.ErrDuplicate) {
			t.Fatalf("Register = %v", err)
		}
	})
	for want, option := range map[commands.DecisionProfile]commands.Option[ReserveSeat]{
		commands.DecisionsUnmarked:    commands.WithName[ReserveSeat]("ReserveSeat"),
		commands.DecisionsProtected:   commands.WithProtectedDecisions[ReserveSeat](),
		commands.DecisionsUnprotected: commands.WithUnprotectedDecisions[ReserveSeat](),
	} {
		var r commands.Registry
		must(t, commands.Register(&r, handler, commands.WithoutModelValidation[ReserveSeat](), option))
		must(t, r.AddDecisionProvider(commands.NewDecisionProvider()))
		addOwner(t, &r)
		registration, err := build(t, &r, commands.PipelineOptions{}).LookupCommand(ReserveSeat{})
		if err != nil || registration.DecisionProfile() != want {
			t.Fatalf("profile = %v, %v; want %v", registration.DecisionProfile(), err, want)
		}
	}
}

func TestProtectedDecisionBuildRequiresCertifiedProviderAndOwner(t *testing.T) {
	for _, name := range []string{"no-provider", "no-owner", "unprotected", "unmarked"} {
		t.Run(name, func(t *testing.T) {
			var r commands.Registry
			profile := commands.WithProtectedDecisions[ReserveSeat]()
			switch name {
			case "unprotected":
				profile = commands.WithUnprotectedDecisions[ReserveSeat]()
			case "unmarked":
				profile = commands.WithoutModelValidation[ReserveSeat]()
			}
			options := []commands.Option[ReserveSeat]{commands.Void(func(ReserveSeat, context.Context) error { return nil }), profile}
			if name != "unmarked" {
				options = append(options, commands.WithoutModelValidation[ReserveSeat]())
			}
			must(t, commands.Register(&r, options...))
			if name != "no-provider" && name != "unprotected" && name != "unmarked" {
				must(t, r.AddDecisionProvider(commands.NewDecisionProvider()))
			}
			if name == "no-provider" {
				addOwner(t, &r)
			}
			_, err := r.Build(commands.PipelineOptions{})
			switch name {
			case "unprotected", "unmarked":
				if err != nil {
					t.Fatalf("advisory profile needs no provider: %v", err)
				}
			default:
				if !errors.Is(err, commands.ErrDecisionProfile) {
					t.Fatalf("Build = %v", err)
				}
				// A failed Build leaves the registry editable; completing the
				// composition admits the profile.
				if name == "no-provider" {
					must(t, r.AddDecisionProvider(commands.NewDecisionProvider()))
				} else {
					addOwner(t, &r)
				}
				build(t, &r, commands.PipelineOptions{})
			}
		})
	}
}

func TestAddDecisionProviderRefusesNilZeroDuplicateAndFrozen(t *testing.T) {
	var r commands.Registry
	provider := commands.NewDecisionProvider()
	if err := r.AddDecisionProvider(nil); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal("nil provider", err)
	}
	if err := (*commands.Registry)(nil).AddDecisionProvider(provider); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal("nil registry", err)
	}
	if err := r.AddDecisionProvider(&commands.DecisionProvider{}); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal("zero provider", err)
	}
	must(t, r.AddDecisionProvider(provider))
	if err := r.AddDecisionProvider(provider); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal("duplicate provider", err)
	}
	build(t, &r, commands.PipelineOptions{})
	if err := r.AddDecisionProvider(commands.NewDecisionProvider()); !errors.Is(err, commands.ErrFrozen) {
		t.Fatal("frozen registry", err)
	}
}

func TestProtectedDecisionReadIsEnrolledAndVerifiedBeforeHandle(t *testing.T) {
	provider, source := commands.NewDecisionProvider(), &seatSource{}
	var handled atomic.Int32
	p := seatPipeline(t, provider, &handled, func(ctx context.Context, inv *commands.Invocation, c ReserveSeat) (commands.DecisionEvidence, error) {
		first, err := commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
		if err != nil {
			return nil, err
		}
		again, err := commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
		if err != nil || again != first {
			t.Errorf("cached read = %v, %v", again, err)
		}
		if _, ok := first.Value().(*Seat); !ok {
			t.Errorf("value = %T", first.Value())
		}
		return first, nil
	}, commands.WithProtectedDecisions[ReserveSeat]())
	result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"})
	if err != nil || !result.IsSuccess() || handled.Load() != 1 {
		t.Fatalf("Execute = %v, %v, handled %d", result, err, handled.Load())
	}
	// One fold, enrollment on each issue, and a provider check at each issue plus
	// the pre-Handle verification of the provided read.
	if source.acquired.Load() != 1 || source.enrolled.Load() != 2 || source.checked.Load() != 4 {
		t.Fatalf("acquired=%d enrolled=%d checked=%d", source.acquired.Load(), source.enrolled.Load(), source.checked.Load())
	}
}

func TestProvidedDecisionReadsAreRefusedBeforeHandle(t *testing.T) {
	for _, name := range []string{"foreign", "nil", "zero", "empty-evidence", "expired", "uncertified-provider", "unmarked-foreign"} {
		t.Run(name, func(t *testing.T) {
			provider, source := commands.NewDecisionProvider(), &seatSource{}
			var handled atomic.Int32
			var retained *commands.DecisionRead
			var calls atomic.Int32
			profile := commands.WithProtectedDecisions[ReserveSeat]()
			if name == "unmarked-foreign" {
				profile = commands.WithName[ReserveSeat]("ReserveSeat")
			}
			p := seatPipeline(t, provider, &handled, func(ctx context.Context, inv *commands.Invocation, c ReserveSeat) (commands.DecisionEvidence, error) {
				if calls.Add(1) == 1 && name != "unmarked-foreign" {
					// The first, successful execution issues a read that later
					// executions retain.
					read, err := commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
					retained = read
					return read, err
				}
				switch name {
				case "nil":
					return (*commands.DecisionRead)(nil), nil
				case "zero":
					return &commands.DecisionRead{}, nil
				case "empty-evidence":
					return emptyEvidence{}, nil
				case "expired":
					read, err := commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
					source.stale.Store(true) // the provider no longer accepts it
					return read, err
				case "uncertified-provider":
					return commands.ReadDecision(ctx, inv, seatTarget(commands.NewDecisionProvider(), c.Seat), source)
				}
				return retained, nil
			}, profile)
			if name != "unmarked-foreign" {
				if result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"}); err != nil || !result.IsSuccess() {
					t.Fatalf("first execution = %v, %v", result, err)
				}
			} else {
				retained = issuedElsewhere(t)
			}
			before := handled.Load()
			result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"})
			if result.IsSuccess() || !errors.Is(err, commands.ErrDecisionRead) {
				t.Fatalf("refused read = %v, %v", result, err)
			}
			if handled.Load() != before {
				t.Fatal("Handle ran with a refused decision read")
			}
		})
	}
}

// issuedElsewhere returns a genuine read issued by another protected pipeline.
func issuedElsewhere(t *testing.T) *commands.DecisionRead {
	t.Helper()
	provider, source := commands.NewDecisionProvider(), &seatSource{}
	var handled atomic.Int32
	var read *commands.DecisionRead
	p := seatPipeline(t, provider, &handled, func(ctx context.Context, inv *commands.Invocation, c ReserveSeat) (commands.DecisionEvidence, error) {
		var err error
		read, err = commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
		return read, err
	}, commands.WithProtectedDecisions[ReserveSeat]())
	if result, err := p.Execute(t.Context(), ReserveSeat{Seat: "B2"}); err != nil || !result.IsSuccess() || read == nil {
		t.Fatalf("issuing execution = %v, %v", result, err)
	}
	return read
}

func TestUnmarkedAndUnprotectedCommandsCannotReadDecisions(t *testing.T) {
	for want, profile := range map[commands.DecisionProfile]commands.Option[ReserveSeat]{
		commands.DecisionsUnmarked:    commands.WithName[ReserveSeat]("ReserveSeat"),
		commands.DecisionsUnprotected: commands.WithUnprotectedDecisions[ReserveSeat](),
	} {
		provider, source := commands.NewDecisionProvider(), &seatSource{}
		var handled atomic.Int32
		p := seatPipeline(t, provider, &handled, func(ctx context.Context, inv *commands.Invocation, c ReserveSeat) (commands.DecisionEvidence, error) {
			got, err := commands.CurrentDecisionProfile(ctx, inv)
			if err != nil || got != want {
				t.Errorf("profile = %v, %v; want %v", got, err, want)
			}
			read, err := commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
			if read != nil || !errors.Is(err, commands.ErrDecisionRead) || !errors.Is(err, commands.ErrDecisionProfile) {
				t.Errorf("read = %v, %v", read, err)
			}
			return nil, nil
		}, profile)
		if result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"}); err != nil || !result.IsSuccess() || handled.Load() != 1 {
			t.Fatalf("advisory command = %v, %v", result, err)
		}
		if source.acquired.Load() != 0 {
			t.Fatal("refused profile reached the provider")
		}
	}
}

func TestDecisionRefusalCannotBeAllowedBySeverity(t *testing.T) {
	provider := commands.NewDecisionProvider()
	var handled atomic.Int32
	p := seatPipeline(t, provider, &handled, func(context.Context, *commands.Invocation, ReserveSeat) (commands.DecisionEvidence, error) {
		return &commands.DecisionRead{}, nil
	}, commands.WithProtectedDecisions[ReserveSeat]())
	allowed := validation.Error
	result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"}, commands.ExecuteOptions{AllowedSeverity: &allowed})
	if result.IsSuccess() || !errors.Is(err, commands.ErrDecisionRead) || handled.Load() != 0 {
		t.Fatalf("allowed severity admitted a refused read: %v, %v", result, err)
	}
}

func TestUnprotectedCommandMayReturnReadlessEvidenceButNotReads(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence func() commands.DecisionEvidence
		want     bool
	}{
		{"advisory-snapshot", func() commands.DecisionEvidence { return emptyEvidence{} }, true},
		{"foreign-read", func() commands.DecisionEvidence { return issuedElsewhere(t) }, false},
		{"zero-read", func() commands.DecisionEvidence { return &commands.DecisionRead{} }, false},
	} {
		for name, profile := range map[string]commands.Option[ReserveSeat]{
			"unprotected": commands.WithUnprotectedDecisions[ReserveSeat](),
			"unmarked":    commands.WithName[ReserveSeat]("ReserveSeat"),
		} {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				var handled atomic.Int32
				p := seatPipeline(t, commands.NewDecisionProvider(), &handled, func(context.Context, *commands.Invocation, ReserveSeat) (commands.DecisionEvidence, error) {
					return tc.evidence(), nil
				}, profile)
				result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"})
				if tc.want && (err != nil || !result.IsSuccess() || handled.Load() != 1) {
					t.Fatalf("advisory evidence = %v, %v, handled %d", result, err, handled.Load())
				}
				if !tc.want && (result.IsSuccess() || !errors.Is(err, commands.ErrDecisionRead) || handled.Load() != 0) {
					t.Fatalf("refused evidence = %v, %v, handled %d", result, err, handled.Load())
				}
			})
		}
	}
}

func TestOverwrittenDecisionReadIsRefusedBeforeHandle(t *testing.T) {
	provider, source := commands.NewDecisionProvider(), &seatSource{}
	foreign := issuedElsewhere(t)
	var handled atomic.Int32
	p := seatPipeline(t, provider, &handled, func(ctx context.Context, inv *commands.Invocation, c ReserveSeat) (commands.DecisionEvidence, error) {
		current, err := commands.ReadDecision(ctx, inv, seatTarget(provider, c.Seat), source)
		if err != nil {
			return nil, err
		}
		*current = *foreign // replace the issued handle's evidence with a foreign read's
		return current, nil
	}, commands.WithProtectedDecisions[ReserveSeat]())
	result, err := p.Execute(t.Context(), ReserveSeat{Seat: "A1"})
	if result.IsSuccess() || !errors.Is(err, commands.ErrDecisionRead) || handled.Load() != 0 {
		t.Fatalf("overwritten read = %v, %v, handled %d", result, err, handled.Load())
	}
}
