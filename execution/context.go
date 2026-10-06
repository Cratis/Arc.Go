// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"time"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

type receiptKey struct{}

// WithReceivedAt installs a UTC receipt without a monotonic component; parents remain unchanged.
func WithReceivedAt(ctx context.Context, receivedAt time.Time) context.Context {
	return context.WithValue(ctx, receiptKey{}, receivedAt.Round(0).UTC())
}

// ReceivedAt returns the receipt and its presence, including an explicitly installed zero time.
func ReceivedAt(ctx context.Context) (time.Time, bool) {
	receipt, ok := ctx.Value(receiptKey{}).(time.Time)
	return receipt, ok
}

// Metadata contains values only, never a provider, scope, request, or cancellation ownership.
type Metadata struct {
	// Principal is the explicit trusted actor; zero means anonymous.
	Principal identity.Principal
	// Tenant is the explicit tenant; zero means NotSet.
	Tenant tenancy.ID
	// CorrelationID identifies the operation; zero requests generation.
	CorrelationID correlation.ID
	// ReceivedAt records dispatch receipt; zero requests the current time.
	ReceivedAt time.Time
}

// Capture copies Arc metadata only, not dependencies or cancellation ownership.
func Capture(ctx context.Context) Metadata {
	principal, _ := identity.PrincipalFrom(ctx)
	tenant, _ := tenancy.TenantFrom(ctx)
	receivedAt, _ := ReceivedAt(ctx)
	return Metadata{Principal: principal, Tenant: tenant, CorrelationID: correlation.FromContext(ctx), ReceivedAt: receivedAt}
}

// NewContext explicitly installs metadata, generating missing correlation and receipt.
// Anonymous/NotSet values do not inherit parent authority. Cancellation remains with ctx.
func NewContext(ctx context.Context, metadata Metadata) (context.Context, error) {
	if metadata.CorrelationID.IsZero() {
		id, err := concepts.NewUUID()
		if err != nil {
			return nil, err
		}
		metadata.CorrelationID = id
	}
	if metadata.ReceivedAt.IsZero() {
		metadata.ReceivedAt = time.Now()
	}
	ctx = identity.WithPrincipal(ctx, metadata.Principal)
	ctx = tenancy.WithTenant(ctx, metadata.Tenant)
	ctx = correlation.WithID(ctx, metadata.CorrelationID)
	return WithReceivedAt(ctx, metadata.ReceivedAt), nil
}
