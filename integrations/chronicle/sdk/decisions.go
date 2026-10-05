// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"reflect"
	"sync"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/readmodels"
	"github.com/cratis/chronicle.go/transactions"
)

// Decisions is the Chronicle decision-read provider of one integration, the Go
// counterpart of C# Arc's command-aware IDecisionReads. Construct it with
// EnableDecisions; the zero value and nil are invalid. It is safe for
// concurrent use by commands built from the registry it was enabled for.
type Decisions struct {
	provider    *commands.DecisionProvider
	integration *integration.Integration
	adapter     *adapter
}

// EnableDecisions certifies i's Chronicle client as a decision-read provider
// for the commands built by builder, so commands registered with
// commands.WithProtectedDecisions can read through ReadDecision. Call it before
// Build, with an integration created by New and installed on the same builder
// (Install supplies the completion owner Build requires). Each call certifies a
// separate provider identity whose reads never mix with another's.
//
// It performs no I/O. A nil or foreign integration returns integration.ErrInvalid.
func EnableDecisions(builder *arc.Builder, i *integration.Integration) (*Decisions, error) {
	if builder == nil || i == nil {
		return nil, integration.ErrInvalid
	}
	a, ok := i.ReadModelProvider().(*adapter)
	if !ok {
		return nil, integration.ErrInvalid
	}
	d := &Decisions{provider: commands.NewDecisionProvider(), integration: i, adapter: a}
	if err := builder.Commands().AddDecisionProvider(d.provider); err != nil {
		return nil, err
	}
	return d, nil
}

// Decision is a typed Chronicle decision read. A protected read is
// invocation-owned evidence that implements commands.DecisionEvidence: return
// it from Provide and Arc verifies it before Handle. An advisory read, served to
// a command marked commands.WithUnprotectedDecisions, carries no evidence and
// guards nothing. The zero value and nil carry no read.
type Decision[M any] struct {
	read     *commands.DecisionRead
	advisory readmodels.Instance[M]
}

// Instance returns the read-model state the decision was made from. Absence is
// explicit; LastHandled is progress, never evidence.
func (d *Decision[M]) Instance() readmodels.Instance[M] {
	if d == nil {
		return readmodels.Instance[M]{}
	}
	if d.read != nil {
		if read, ok := d.read.Value().(*decisionRead[M]); ok {
			return read.instance
		}
		return readmodels.Instance[M]{}
	}
	return d.advisory
}

// IsProtected reports whether the decision is enrolled, invocation-owned evidence.
func (d *Decision[M]) IsProtected() bool { return d != nil && d.read != nil }

// DecisionReads implements commands.DecisionEvidence. An advisory decision
// carries no reads, which Arc accepts only for unprotected commands.
func (d *Decision[M]) DecisionReads() []*commands.DecisionRead {
	if d == nil || d.read == nil {
		return nil
	}
	return []*commands.DecisionRead{d.read}
}

// ReadDecision reads model M at key for the command that owns inv, within the
// store and namespace the integration routes that command to.
//
// For a command marked commands.WithProtectedDecisions it issues a protected
// read through commands.ReadDecision: classified, reducer-backed and otherwise
// unsupported models are refused before any acquisition; the read is shared per
// invocation and target; outside validation-only execution it is enrolled with
// the command's Chronicle transaction, so a competing append rejects the whole
// batch at commit. Validation-only execution reads separately and never enrolls.
// For a command marked commands.WithUnprotectedDecisions it returns an advisory
// snapshot from the ordinary read-model reader. An unmarked command is refused.
// Every refusal of a protected read wraps commands.ErrDecisionRead.
//
// The model must be registered with the client's store; otherwise it returns
// integration.ErrNotRegistered.
func ReadDecision[M any](ctx context.Context, inv *commands.Invocation, d *Decisions, model readmodels.Model[M], key readmodels.Key) (*Decision[M], error) {
	if ctx == nil || inv == nil || d == nil || d.provider == nil || d.adapter == nil || key == "" {
		return nil, integration.ErrInvalid
	}
	typ := reflect.TypeFor[M]()
	if model.Descriptor().GoType() != typ {
		return nil, integration.ErrInvalid
	}
	if descriptor, found := d.adapter.models.LookupType(typ); !found || descriptor.Identifier() != model.Identifier() {
		return nil, integration.ErrNotRegistered
	}
	profile, err := commands.CurrentDecisionProfile(ctx, inv)
	if err != nil {
		return nil, err
	}
	coordinates, err := d.integration.CoordinatesFor(ctx, inv)
	if err != nil {
		return nil, err
	}
	if profile == commands.DecisionsUnprotected {
		store, err := d.store(ctx, coordinates)
		if err != nil {
			return nil, err
		}
		instance, err := readmodels.For(store.ReadModels(), model).Get(auditContext(ctx), key)
		if err != nil {
			return nil, err
		}
		return &Decision[M]{advisory: instance}, nil
	}
	source := &decisionSource[M]{decisions: d, inv: inv, model: model, key: key, coordinates: coordinates}
	target := commands.DecisionTarget{Provider: d.provider, Model: typ, Store: string(coordinates.Store), Namespace: string(coordinates.Namespace), Key: string(key)}
	read, err := commands.ReadDecision(ctx, inv, target, source)
	if err != nil {
		return nil, err
	}
	return &Decision[M]{read: read}, nil
}

func (d *Decisions) store(ctx context.Context, coordinates integration.Coordinates) (*chronicle.EventStore, error) {
	if string(coordinates.Store) != string(d.adapter.store) {
		return nil, integration.ErrMismatch
	}
	return d.adapter.client.EventStore(ctx, d.adapter.store, chronicle.WithNamespace(chronicle.Namespace(coordinates.Namespace)))
}

// decisionSource is the commands.DecisionSource for one target. Arc calls Admit
// and Acquire only for the first caller of a target in a command frame; Check
// and Enroll run for every issue and verification.
type decisionSource[M any] struct {
	decisions   *Decisions
	inv         *commands.Invocation
	model       readmodels.Model[M]
	key         readmodels.Key
	coordinates integration.Coordinates

	mu       sync.Mutex
	provider *decisionProvider[M]
}

// Admit refuses unsupported models from the frozen catalog before any lease,
// fold or enrollment. Obtaining the namespace's store may connect the client.
func (s *decisionSource[M]) Admit(ctx context.Context) error {
	store, err := s.decisions.store(ctx, s.coordinates)
	if err != nil {
		return err
	}
	provider := newDecisionProvider(store.ReadModels(), s.model)
	if err := provider.admit(); err != nil {
		return err
	}
	s.mu.Lock()
	s.provider = &provider
	s.mu.Unlock()
	return nil
}

// Acquire folds the instance detached; enrollment is a separate stage so that
// validation-only execution never touches the completion owner.
func (s *decisionSource[M]) Acquire(ctx context.Context) (any, error) {
	s.mu.Lock()
	provider := s.provider
	s.mu.Unlock()
	if provider == nil {
		return nil, integration.ErrInvalid
	}
	return provider.detached(ctx, s.key)
}

// Check accepts only a read this provider issued for this target. Token
// lifetime (connection generation, catalog epoch, owner) is validated by the
// SDK on every enrollment and again before the commit is dispatched.
func (s *decisionSource[M]) Check(ctx context.Context, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	read, ok := value.(*decisionRead[M])
	if !ok || read == nil || read.token.IsZero() {
		return transactions.ErrInvalidDecision
	}
	return nil
}

// Enroll registers the read with the command's Chronicle transaction.
func (s *decisionSource[M]) Enroll(ctx context.Context, value any) error {
	if err := s.Check(ctx, value); err != nil {
		return err
	}
	return s.decisions.integration.EnrollDecision(ctx, s.inv, value)
}
