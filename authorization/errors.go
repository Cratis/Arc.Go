// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization

import (
	"errors"
	"fmt"
)

var (
	// ErrDenied identifies ordinary authorization denial.
	ErrDenied = errors.New("authorization denied")
	// ErrInvalidConfiguration identifies malformed declaration or registry input.
	ErrInvalidConfiguration = errors.New("invalid authorization configuration")
	// ErrDuplicate identifies duplicate policies or artifact identities.
	ErrDuplicate = errors.New("duplicate authorization registration")
	// ErrFrozen identifies changes after successful Build.
	ErrFrozen = errors.New("authorization registry frozen")
	// ErrUnknownPolicy identifies an unregistered policy name.
	ErrUnknownPolicy = errors.New("unknown authorization policy")
	// ErrUnsupportedScheme identifies unsupported native authentication schemes.
	ErrUnsupportedScheme = errors.New("authentication schemes unsupported")
	// ErrUnknownTarget identifies a target absent from the compiled catalog.
	ErrUnknownTarget = errors.New("unknown authorization target")
	// ErrIdentityChanged identifies replacement of principal/tenant or their presence.
	ErrIdentityChanged = errors.New("authorization identity changed")
	// ErrNotPrepared identifies a zero Prepared value.
	ErrNotPrepared = errors.New("authorization not prepared")
)

// ConfigurationError describes configuration identities, never request values.
type ConfigurationError struct {
	// Target is the malformed artifact, or zero for registry configuration.
	Target Target
	// Policy is the declaration's policy identity, if relevant.
	Policy string
	// Kind is the inspectable failure category.
	Kind error
}

// Error describes the configuration identity and failure category.
func (e *ConfigurationError) Error() string {
	return fmt.Sprintf("authorization configuration for %q policy %q: %v", e.Target.Identity, e.Policy, e.Kind)
}

// Unwrap preserves the category and general configuration failure identity.
func (e *ConfigurationError) Unwrap() []error { return []error{ErrInvalidConfiguration, e.Kind} }

func configuration(target Target, policy string, kind error) error {
	return &ConfigurationError{Target: target, Policy: policy, Kind: kind}
}
