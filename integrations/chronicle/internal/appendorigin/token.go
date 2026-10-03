// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package appendorigin carries provider-owned attribution metadata, not an
// execution capability or a context decorator. The behavior package stays SDK-independent.
package appendorigin

import "context"

// Name is the integration-owned command snapshot entry.
const Name = "chronicle.appendOrigin"

// Provider allocates one immutable provider-typed token per root execution.
// Tokens must not retain contexts or carry transaction capabilities.
type Provider interface {
	NewAppendOrigin() any
}

type key struct{}

// WithToken carries the current filter's token to subscription setup. SetValue
// only affects subsequent callbacks, so the filter snapshot is not yet updated.
func WithToken(ctx context.Context, token any) context.Context {
	return context.WithValue(ctx, key{}, token)
}

// From returns subscription setup metadata; it does not inspect inherited SDK origins.
func From(ctx context.Context) any { return ctx.Value(key{}) }
