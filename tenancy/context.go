// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy

import "context"

type tenantKey struct{}

// WithTenant installs tenant metadata; zero explicitly shadows an inherited tenant.
func WithTenant(ctx context.Context, tenant ID) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenant)
}

// TenantFrom returns the tenant and its presence, including explicit NotSet.
func TenantFrom(ctx context.Context) (ID, bool) { id, ok := ctx.Value(tenantKey{}).(ID); return id, ok }
