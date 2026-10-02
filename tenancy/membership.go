// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy

import (
	"context"

	"github.com/cratis/arc.go/identity"
)

// Membership independently authorizes a principal for a selected tenant. Hosts
// must enforce configured membership even for public operations. Selection alone
// is never approval; shared implementations must support concurrent calls.
type Membership interface {
	Authorize(context.Context, identity.Principal, ID) (bool, error)
}

// MembershipFunc adapts a synchronous, cancellation-aware membership callback.
type MembershipFunc func(context.Context, identity.Principal, ID) (bool, error)

// Authorize invokes f; it does not change context or selection.
func (f MembershipFunc) Authorize(ctx context.Context, principal identity.Principal, tenant ID) (bool, error) {
	return f(ctx, principal, tenant)
}

// Require rejects NotSet only; the named Default satisfies the requirement.
func Require(id ID) error {
	if !id.IsSet() {
		return ErrNotSet
	}
	return nil
}
