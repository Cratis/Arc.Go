// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization

import (
	"slices"

	"github.com/cratis/arc.go/metadata"
)

type requirement struct {
	roles        []string
	registration registration
}
type declaration struct {
	public       bool
	guest        bool
	requirements []requirement
}

func (r *Registry) compile(target Target, source *metadata.Authorization) (declaration, error) {
	if source == nil {
		return declaration{public: true}, nil
	}
	if source.AllowAnonymous && len(source.Requirements) > 0 {
		return declaration{}, configuration(target, "", ErrInvalidConfiguration)
	}
	compiled := declaration{public: source.AllowAnonymous, guest: len(source.Requirements) > 0}
	for _, item := range source.Requirements {
		for _, role := range item.Roles {
			if !validName(role) {
				return declaration{}, configuration(target, item.Policy, ErrInvalidConfiguration)
			}
		}
		if item.Policy != "" && !validName(item.Policy) {
			return declaration{}, configuration(target, item.Policy, ErrInvalidConfiguration)
		}
		for _, scheme := range item.AuthenticationSchemes {
			if !validName(scheme) {
				return declaration{}, configuration(target, item.Policy, ErrInvalidConfiguration)
			}
		}
		if len(item.AuthenticationSchemes) > 0 {
			return declaration{}, configuration(target, item.Policy, ErrUnsupportedScheme)
		}
		registration := registration{}
		if item.Policy != "" {
			var exists bool
			registration, exists = r.policies[item.Policy]
			if !exists {
				return declaration{}, configuration(target, item.Policy, ErrUnknownPolicy)
			}
		}
		if len(item.Roles) > 0 || item.Policy == "" || !registration.anonymous {
			compiled.guest = false
		}
		compiled.requirements = append(compiled.requirements, requirement{roles: slices.Clone(item.Roles), registration: registration})
	}
	return compiled, nil
}
