// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
)

// Kind identifies an operation category.
type Kind uint8

const (
	// Command identifies a command artifact.
	Command Kind = iota + 1
	// Query identifies a read-model query method.
	Query
)

// Target identifies exactly one compiled catalog artifact.
type Target struct {
	// Kind is Command or Query.
	Kind Kind
	// Identity is the exact namespace-qualified artifact identity.
	Identity string
}

// Context is immutable operation metadata plus borrowed, operation-specific data.
// Policies must not mutate or retain Resource.
type Context struct {
	// Principal is the authenticated actor snapshot, or empty for a guest.
	Principal identity.Principal
	// Tenant is the selected tenant, not evidence of membership.
	Tenant tenancy.ID
	// Target identifies the command or query.
	Target Target
	// Resource is borrowed for this evaluation only.
	Resource any
	// ReceivedAt is the operation dispatch receipt, if installed.
	ReceivedAt time.Time
}

// Policy evaluates synchronously and must honor cancellation. Shared policies
// must support concurrent use. Unexpected panics propagate to pipeline boundaries.
type Policy interface {
	Authorize(context.Context, Context) (Decision, error)
}

// PolicyFunc adapts a policy callback.
type PolicyFunc func(context.Context, Context) (Decision, error)

// Authorize invokes f with borrowed operation data.
func (f PolicyFunc) Authorize(ctx context.Context, value Context) (Decision, error) {
	return f(ctx, value)
}

// PolicyOptions controls whether policy-only declarations may evaluate guests.
type PolicyOptions struct {
	// EvaluatesAnonymous explicitly opts this policy into guest evaluation.
	EvaluatesAnonymous bool
}

// Options configures frozen application fallback declarations.
type Options struct {
	// Fallback applies only to undeclared operations and is copied by Build.
	Fallback *metadata.Authorization
}

// Registry is a single-owner builder; zero is ready for use. Successful Build
// freezes it. Build validates configuration without constructing any policy.
type Registry struct {
	policies map[string]registration
	frozen   bool
}
type registration struct {
	policy    Policy
	factory   func(context.Context, *execution.Scope) (Policy, error)
	anonymous bool
}

// Factory constructs a borrowed policy only during scoped evaluation. Its scope
// view is non-closing and expires on return from EvaluateScoped. DI dependency
// manifests will follow the published Fundamentals contracts.
type Factory[P Policy] func(context.Context, *execution.Scope) (P, error)

// RegisterPolicy registers a lazy scoped policy without activating its factory.
// Nil factories and duplicate names fail. Resources own disposal of the policy.
func RegisterPolicy[P Policy](r *Registry, name string, factory Factory[P], options PolicyOptions) error {
	if factory == nil {
		return configuration(Target{}, name, ErrInvalidConfiguration)
	}
	return r.register(name, registration{anonymous: options.EvaluatesAnonymous, factory: func(ctx context.Context, scope *execution.Scope) (Policy, error) {
		policy, err := factory(ctx, scope)
		if check := scope.CheckContext(ctx); check != nil {
			return nil, errors.Join(err, check)
		}
		if err != nil {
			return nil, err
		}
		if nilValue(policy) {
			return nil, ErrInvalidConfiguration
		}
		return policy, nil
	}})
}

// Register borrows a concurrently callable direct policy. Names are exact and
// nonempty, with no surrounding whitespace or control characters.
func (r *Registry) Register(name string, policy Policy, options PolicyOptions) error {
	if nilValue(policy) {
		return configuration(Target{}, name, ErrInvalidConfiguration)
	}
	return r.register(name, registration{policy: policy, anonymous: options.EvaluatesAnonymous})
}

func (r *Registry) register(name string, value registration) error {
	if r == nil {
		return configuration(Target{}, name, ErrInvalidConfiguration)
	}
	if r.frozen {
		return ErrFrozen
	}
	if !validName(name) {
		return configuration(Target{}, name, ErrInvalidConfiguration)
	}
	if _, exists := r.policies[name]; exists {
		return configuration(Target{}, name, ErrDuplicate)
	}
	if r.policies == nil {
		r.policies = make(map[string]registration)
	}
	r.policies[name] = value
	return nil
}

// Build validates every declaration, including overridden levels, and copies the
// effective graph. Commands use command then fallback; queries use method then
// read model then fallback. Queries of the same read model must supply identical
// read-model declarations by content. No declaration and no fallback is public.
func (r *Registry) Build(catalog metadata.Catalog, options Options) (*Evaluator, error) {
	if r == nil {
		return nil, configuration(Target{}, "", ErrInvalidConfiguration)
	}
	if r.frozen {
		return nil, ErrFrozen
	}
	if catalog.Version != metadata.Version {
		return nil, configuration(Target{}, "", ErrInvalidConfiguration)
	}
	fallback, err := r.compile(Target{}, options.Fallback)
	if err != nil {
		return nil, err
	}
	type artifact struct {
		target    Target
		levels    []*metadata.Authorization
		valid     bool
		readModel string
	}
	artifacts := make([]artifact, 0, len(catalog.Commands)+len(catalog.Queries))
	for _, command := range catalog.Commands {
		artifacts = append(artifacts, artifact{target: Target{Kind: Command, Identity: command.Type.Identity()}, levels: []*metadata.Authorization{command.Authorization}, valid: validType(command.Type)})
	}
	for _, query := range catalog.Queries {
		artifacts = append(artifacts, artifact{target: Target{Kind: Query, Identity: query.Identity()}, levels: []*metadata.Authorization{query.Authorization, query.ReadModelAuthorization}, valid: validType(query.ReadModel) && validSegment(query.Name), readModel: query.ReadModel.Identity()})
	}
	slices.SortFunc(artifacts, func(a, b artifact) int {
		if n := strings.Compare(a.target.Identity, b.target.Identity); n != 0 {
			return n
		}
		return int(a.target.Kind) - int(b.target.Kind)
	})
	readModels := make(map[string]*metadata.Authorization)
	evaluator := &Evaluator{declarations: make(map[Target]declaration), sources: make(map[Target][]*metadata.Authorization)}
	for i, artifact := range artifacts {
		if !artifact.valid || !validTarget(artifact.target) {
			return nil, configuration(artifact.target, "", ErrInvalidConfiguration)
		}
		if i > 0 && artifacts[i-1].target.Identity == artifact.target.Identity {
			return nil, configuration(artifact.target, "", ErrDuplicate)
		}
		if artifact.target.Kind == Query {
			readModelDeclaration := artifact.levels[1]
			if previous, exists := readModels[artifact.readModel]; exists && !sameAuthorization(previous, readModelDeclaration) {
				return nil, configuration(artifact.target, "", ErrInvalidConfiguration)
			}
			readModels[artifact.readModel] = readModelDeclaration
		}
		effective := fallback
		selected := false
		for _, level := range artifact.levels {
			compiled, err := r.compile(artifact.target, level)
			if err != nil {
				return nil, err
			}
			if level != nil && !selected {
				effective = compiled
				selected = true
			}
		}
		evaluator.declarations[artifact.target] = effective
		for _, level := range artifact.levels {
			evaluator.sources[artifact.target] = append(evaluator.sources[artifact.target], cloneAuthorization(level))
		}
	}
	r.frozen = true
	return evaluator, nil
}
func validName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func validTarget(target Target) bool {
	if target.Kind != Command && target.Kind != Query {
		return false
	}
	for _, part := range strings.Split(target.Identity, ".") {
		if !validSegment(part) {
			return false
		}
	}
	return true
}
func validSegment(part string) bool {
	if part == "" {
		return false
	}
	for i, c := range part {
		if c != '_' && !unicode.IsLetter(c) && (i == 0 || !unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}
func validType(value metadata.TypeName) bool {
	if !validSegment(value.Name) {
		return false
	}
	if value.Namespace != "" {
		for _, part := range strings.Split(value.Namespace, ".") {
			if !validSegment(part) {
				return false
			}
		}
	}
	return true
}
func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}
