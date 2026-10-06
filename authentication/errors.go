// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authentication

import "errors"

var (
	// ErrFailed identifies recognized rejected credentials.
	ErrFailed = errors.New("authentication failed")
	// ErrInvalidHandler identifies a nil or typed-nil handler.
	ErrInvalidHandler = errors.New("invalid authentication handler")
	// ErrInvalidPrincipal identifies an anonymous success principal.
	ErrInvalidPrincipal = errors.New("invalid authenticated principal")
	// ErrInvalidRequest identifies a nil request, context or chain.
	ErrInvalidRequest = errors.New("invalid authentication request")
)
