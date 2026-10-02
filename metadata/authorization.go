// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metadata

// AuthorizationRequirement is one conjunctive restriction. Roles within it are
// alternatives (OR); multiple requirements are AND. Names are exact strings.
// Executable validation belongs to authorization.Registry.Build, not Resolve.
type AuthorizationRequirement struct {
	// Roles lists explicit role alternatives, not comma-separated syntax.
	Roles []string `json:"roles,omitempty"`
	// Policy names a registered authorization policy.
	Policy string `json:"policy,omitempty"`
	// AuthenticationSchemes describes named schemes; native Go rejects these.
	AuthenticationSchemes []string `json:"authenticationSchemes,omitempty"`
}

// Authorization is explicit declaration metadata. A nil declaration means
// undeclared; a nonnil empty declaration requires authentication. AllowAnonymous
// contradicts any requirements and suppresses application fallback.
type Authorization struct {
	// AllowAnonymous explicitly permits anonymous access to this operation.
	AllowAnonymous bool `json:"allowAnonymous"`
	// Requirements combines restrictions in declaration order.
	Requirements []AuthorizationRequirement `json:"requirements,omitempty"`
}
