// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/readmodels"
	"github.com/cratis/chronicle.go/transactions"
)

// decisionProvider holds the provider primitives behind EnableDecisions. New
// does not register them; protected command support is an explicit opt-in.
// A concrete SDK reader is intentional: arbitrary readers and ModelDocument's
// LastHandled progress must never be promoted to optimistic evidence.
type decisionProvider[T any] struct {
	reader *readmodels.DecisionReader[T]
	model  readmodels.Identifier
}

type decisionRead[T any] struct {
	instance readmodels.Instance[T]
	token    transactions.DecisionToken
}

// decisionEvidence is implemented only by SDK-issued reads, so application code
// cannot hand the participant a token envelope of its own.
type decisionEvidence interface {
	decisionToken() transactions.DecisionToken
}

func (r *decisionRead[T]) decisionToken() transactions.DecisionToken {
	if r == nil {
		return transactions.DecisionToken{}
	}
	return r.token
}

func newDecisionProvider[T any](service *readmodels.Service, model readmodels.Model[T]) decisionProvider[T] {
	return decisionProvider[T]{reader: readmodels.DecisionsFor(service, model), model: model.Descriptor().Identifier()}
}

func (p decisionProvider[T]) admit() error {
	admission := p.reader.Admit()
	if !admission.IsAdmitted {
		return &readmodels.DecisionReadRefused{Model: p.model, Reason: admission.Reason}
	}
	return nil
}

// detached is also the validation-only path. It never enrolls an ambient unit,
// creates an owner or releases model data a second time. SDK 9e0c8d5 owns session
// cleanup and refuses classified models before acquiring a lease/RPC.
func (p decisionProvider[T]) detached(ctx context.Context, key readmodels.Key) (*decisionRead[T], error) {
	if ctx == nil {
		return nil, integration.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.admit(); err != nil {
		return nil, err
	}
	read, err := p.reader.GetDetached(auditContext(ctx), key)
	if err != nil {
		return nil, err
	}
	if read.Token.IsZero() {
		return nil, transactions.ErrInvalidDecision
	}
	return &decisionRead[T]{instance: read.Instance, token: read.Token}, nil
}

// acquire requires the actual Arc SDK participant before folding. Enrollment is
// against that participant, never transactions.FromContext. Its completion owner
// stays exclusively in the existing completion object.
func (p decisionProvider[T]) acquire(ctx context.Context, key readmodels.Key, target *participant) (*decisionRead[T], error) {
	if err := decisionParticipant(target); err != nil {
		return nil, err
	}
	read, err := p.detached(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := enrollDecision(ctx, target, read); err != nil {
		return nil, err
	}
	return read, nil
}

func decisionParticipant(p *participant) error {
	if p == nil || p.unit == nil || p.coordinates.Sequence != integration.SequenceID(events.EventLog) {
		return integration.ErrUnsupported
	}
	switch p.unit.State() {
	case transactions.Open:
		return nil
	case transactions.Completing:
		return transactions.ErrCompleting
	case transactions.Invalid:
		return integration.ErrUnsupported
	default:
		return transactions.ErrCompleted
	}
}

// enrollDecision deliberately does not accept Instance, ModelDocument or an
// application-created token envelope. The SDK validates zero, target, lifetime,
// permanent owner membership and scope conflicts on every enrollment.
func enrollDecision[T any](ctx context.Context, p *participant, read *decisionRead[T]) error {
	if read == nil {
		return p.EnrollDecision(ctx, nil)
	}
	return p.EnrollDecision(ctx, read)
}

// EnrollDecision implements integration.DecisionParticipant. Only an SDK-issued
// read is accepted; enrollment is against this participant's unit, never
// transactions.FromContext, and repeated enrollment of one token is idempotent.
func (p *participant) EnrollDecision(ctx context.Context, evidence any) error {
	if ctx == nil {
		return integration.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := decisionParticipant(p); err != nil {
		return err
	}
	read, ok := evidence.(decisionEvidence)
	if !ok || read.decisionToken().IsZero() {
		return transactions.ErrInvalidDecision
	}
	return p.unit.Enroll(read.decisionToken())
}
