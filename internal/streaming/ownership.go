// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"context"
	"crypto/subtle"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

// ConnectionOwner freezes verified subject and tenant ownership, not display
// names, roles or a bearer token. Anonymous evidence is host/session or per-
// connection cookie evidence, separate from the public connection ID.
type ConnectionOwner struct {
	authenticated, principalPresent, tenantPresent bool
	subject                                        string
	tenant                                         tenancy.ID
	anonymous                                      string
}

// NewConnectionOwner requires a stable nonempty subject for authenticated callers
// and nonempty independent ownership evidence for anonymous callers. Tenant value
// and presence are both retained, so absent, NotSet and Default are not conflated.
func NewConnectionOwner(ctx context.Context, anonymousEvidence string) (ConnectionOwner, error) {
	if ctx == nil {
		return ConnectionOwner{}, ErrControl
	}
	principal, pp := identity.PrincipalFrom(ctx)
	tenant, tp := tenancy.TenantFrom(ctx)
	o := ConnectionOwner{authenticated: principal.IsAuthenticated(), principalPresent: pp, tenantPresent: tp, tenant: tenant}
	if o.authenticated {
		if principal.ID() == "" {
			return ConnectionOwner{}, ErrControl
		}
		o.subject = principal.ID()
	} else {
		if anonymousEvidence == "" {
			return ConnectionOwner{}, ErrControl
		}
		o.anonymous = anonymousEvidence
	}
	return o, nil
}

// Equal compares stable identity and exact tenant ownership. Authenticated owners
// ignore anonymous evidence. Display/role changes do not change subject ownership;
// each new subscription still performs admission with its verified request identity.
func (o ConnectionOwner) Equal(other ConnectionOwner) bool {
	if o.authenticated != other.authenticated || o.principalPresent != other.principalPresent || o.tenantPresent != other.tenantPresent || o.tenant != other.tenant {
		return false
	}
	if o.authenticated {
		return o.subject != "" && o.subject == other.subject
	}
	return o.anonymous != "" && subtle.ConstantTimeCompare([]byte(o.anonymous), []byte(other.anonymous)) == 1
}
