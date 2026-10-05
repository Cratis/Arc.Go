// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"slices"
)

// ErrDecisionProfile identifies a command, registry or invocation that does not
// satisfy the protected decision-read profile. It is returned by Register, Build
// and, joined with ErrDecisionRead, by ReadDecision.
var ErrDecisionProfile = errors.New("arc: protected decision profile refused")

// DecisionProfile is a command's frozen decision-read profile. The zero value is
// DecisionsUnmarked, so existing commands keep their behavior.
type DecisionProfile uint8

const (
	// DecisionsUnmarked is the default profile. ReadDecision refuses; a provider
	// should also refuse its decision-read API, as C# Arc does for unmarked
	// commands, while ordinary read-model injection stays advisory.
	DecisionsUnmarked DecisionProfile = iota
	// DecisionsProtected admits ReadDecision: reads are invocation-owned, enrolled
	// with the provider's completion owner and verified before Handle.
	DecisionsProtected
	// DecisionsUnprotected acknowledges advisory, unguarded reads. ReadDecision
	// refuses; a provider serves its detached snapshot instead.
	DecisionsUnprotected
)

// WithProtectedDecisions opts a command into protected decision reads, the Go
// counterpart of C# [ProtectedDecision]. The profile is fixed at registration,
// before any filter, validator or handler can run.
//
// This initial profile cannot certify arbitrary validation code. Register refuses
// it unless WithoutModelValidation is also supplied, and it refuses any
// WithValidator or WithScopedValidator option and any command operation. Build
// refuses it unless the registry has a decision provider (AddDecisionProvider)
// and a completion owner (AddDeferredCommitParticipant). It cannot be combined
// with WithUnprotectedDecisions (ErrDuplicate).
func WithProtectedDecisions[C any]() Option[C] {
	return option[C]{"decision-profile", func(c *configuration[C]) error { c.decisions = DecisionsProtected; return nil }}
}

// WithUnprotectedDecisions marks a command as using advisory, unguarded reads,
// the Go counterpart of C# [Unprotected]. It adds no runtime guarantee; it lets a
// provider serve detached snapshots and records the choice for tooling. It cannot
// be combined with WithProtectedDecisions (ErrDuplicate).
func WithUnprotectedDecisions[C any]() Option[C] {
	return option[C]{"decision-profile", func(c *configuration[C]) error { c.decisions = DecisionsUnprotected; return nil }}
}

// DecisionProfile returns the frozen decision-read profile.
func (r Registration) DecisionProfile() DecisionProfile { return r.decisions }

// checkDecisionRegistration is the pure, callback-free profile admission. Model
// methods, registered graph rules, tags and custom or scoped validators cannot be
// certified, and operations are not part of this profile.
func checkDecisionRegistration(r Registration) error {
	switch r.decisions {
	case DecisionsUnmarked, DecisionsUnprotected:
		return nil
	case DecisionsProtected:
		if r.operations || !r.withoutModel || len(r.validators) != 0 {
			return ErrDecisionProfile
		}
		return nil
	}
	return ErrDecisionProfile
}

// DecisionProvider is the opaque identity of one decision-read provider, such as
// one Chronicle client. Construct it with NewDecisionProvider; the zero value and
// nil are invalid. Identity is by pointer: two providers never share reads even
// when their store and namespace names match. It carries no capability itself;
// it is certified only for a registry it was added to.
type DecisionProvider struct{ _ byte }

// NewDecisionProvider allocates an independent provider identity.
func NewDecisionProvider() *DecisionProvider { return &DecisionProvider{} }

// AddDecisionProvider certifies a provider identity for protected commands built
// from this registry. Registration is composition, not a runtime boolean: a read
// whose target names any other provider is refused. Nil returns
// ErrInvalidRegistration, a repeated provider ErrDuplicate and a frozen registry
// ErrFrozen. It activates nothing.
func (r *Registry) AddDecisionProvider(provider *DecisionProvider) error {
	if r == nil || provider == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	if slices.Contains(r.decisionProviders, provider) {
		return ErrDuplicate
	}
	r.decisionProviders = append(r.decisionProviders, provider)
	return nil
}

// checkDecisionSupport refuses protected commands without a certified provider
// or a completion owner. It runs during Build, before any factory is activated.
func (r *Registry) checkDecisionSupport() error {
	for _, registration := range r.registrations {
		if registration.decisions != DecisionsProtected {
			continue
		}
		if len(r.decisionProviders) == 0 || len(r.terminal) == 0 {
			return &RegistrationError{registration.descriptor.Type.Identity(), ErrDecisionProfile}
		}
	}
	return nil
}

func decisionProviderSet(providers []*DecisionProvider) map[*DecisionProvider]struct{} {
	set := make(map[*DecisionProvider]struct{}, len(providers))
	for _, provider := range providers {
		set[provider] = struct{}{}
	}
	return set
}

// CurrentDecisionProfile reports the decision profile of the command that owns
// this invocation, after checking callback and security continuity. Providers
// use it to refuse decision reads for unmarked commands and to serve advisory
// snapshots for unprotected ones.
func CurrentDecisionProfile(ctx context.Context, inv *Invocation) (profile DecisionProfile, err error) {
	err = withState(ctx, inv, func(e *Execution) error {
		profile = e.frame.registration.decisions
		return nil
	})
	return profile, err
}
