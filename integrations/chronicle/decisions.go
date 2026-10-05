// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"

	"github.com/cratis/arc.go/commands"
)

// DecisionParticipant is implemented by a provider Participant that can enroll
// its own opaque decision evidence with the transaction's completion owner. The
// provider must refuse evidence it did not issue, and its completion owner must
// reject the complete batch when a competing write invalidates enrolled evidence.
type DecisionParticipant interface {
	Participant
	EnrollDecision(context.Context, any) error
}

// EnrollDecision enrolls provider-issued decision evidence with the command's
// transaction, beginning the provider transaction on first use. It is the
// completion-owner half of a commands.DecisionSource; applications use the
// provider's typed decision API instead of calling it.
//
// It refuses validation-only execution (commands.ErrExecutionMismatch), a
// changed store, namespace or actor (ErrMismatch), and a participant that does
// not implement DecisionParticipant (ErrUnsupported). Concurrent enrollments
// within one invocation are serialized. Any failure, including a provider
// refusal, poisons the transaction so it rolls back instead of committing work
// without its guard.
func (i *Integration) EnrollDecision(ctx context.Context, inv *commands.Invocation, evidence any) (err error) {
	if i == nil || ctx == nil || isNil(evidence) {
		return ErrInvalid
	}
	tx, err := i.transaction(ctx, inv)
	if err != nil {
		return err
	}
	tx.enrolling.Lock()
	defer tx.enrolling.Unlock()
	if err = tx.enter(); err != nil {
		tx.poison(err)
		return err
	}
	defer tx.leave()
	defer func() { tx.poison(err) }()
	frame, err := i.frameFor(ctx, inv)
	if err != nil {
		return err
	}
	if err = i.bind(ctx, tx, frame); err != nil {
		return err
	}
	if err = inv.Execution().Check(ctx); err != nil {
		return err
	}
	participant, ok := tx.participant.(DecisionParticipant)
	if !ok {
		return ErrUnsupported
	}
	return participant.EnrollDecision(frame.context(ctx), evidence)
}

// bind begins the provider transaction on first use and refuses a frame whose
// coordinates or actor differ from the bound transaction. The caller holds busy.
func (i *Integration) bind(ctx context.Context, tx *transaction, frame *commandFrame) (err error) {
	if tx.bound && (tx.coordinates != frame.coordinates || tx.actor != frame.actor) {
		return ErrMismatch
	}
	if tx.owner != nil {
		return nil
	}
	tx.participant, tx.owner, err = i.options.Transactions.Begin(frame.context(ctx), frame.coordinates)
	if err != nil {
		return err
	}
	if isNil(tx.participant) || isNil(tx.owner) {
		return ErrInvalid
	}
	tx.mu.Lock()
	tx.coordinates, tx.actor, tx.bound = frame.coordinates, frame.actor, true
	tx.mu.Unlock()
	return nil
}
