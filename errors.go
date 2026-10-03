// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidOptions identifies invalid construction or registration options.
	ErrInvalidOptions = errors.New("invalid Arc options")
	// ErrFrozen identifies mutation after the root's single Build attempt.
	ErrFrozen = errors.New("Arc builder frozen")
	// ErrNotStarted identifies admission before Start completes.
	ErrNotStarted = errors.New("Arc application not started")
	// ErrShuttingDown identifies admission after shutdown begins.
	ErrShuttingDown = errors.New("Arc application shutting down")
	// ErrStopped identifies an application which cannot restart.
	ErrStopped = errors.New("Arc application stopped")
	// ErrAlreadyServing identifies a second owned server.
	ErrAlreadyServing = errors.New("Arc application already serving")
	// ErrRouteConflict identifies overlapping HTTP ownership.
	ErrRouteConflict = errors.New("Arc route conflict")
	// ErrAuthenticationSetup identifies discovery requiring unavailable authentication.
	ErrAuthenticationSetup = errors.New("Arc discovery authentication unavailable")
	// ErrSchemaUnavailable identifies a shape requiring an explicit schema override.
	ErrSchemaUnavailable = errors.New("Arc schema unavailable")
)

// ConfigurationError identifies a composition failure and preserves its cause.
type ConfigurationError struct {
	Component, Name string
	Cause           error
}

// Error describes the failed composition component.
func (e *ConfigurationError) Error() string {
	return fmt.Sprintf("Arc %s %q: %v", e.Component, e.Name, e.Cause)
}

// Unwrap preserves inspectable configuration error identities.
func (e *ConfigurationError) Unwrap() error { return e.Cause }
