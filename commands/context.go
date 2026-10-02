// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
)

// CommandContext is an immutable metadata snapshot. Command, response and value
// objects remain borrowed; callers must not mutate or retain them beyond execution.
type CommandContext struct {
	descriptor     metadata.Command
	command        any
	correlation    correlation.ID
	received       time.Time
	principal      identity.Principal
	tenant         tenancy.ID
	values         ContextValues
	allowed        *validation.Severity
	validationOnly bool
	response       any
	hasResponse    bool
}

// Descriptor returns a copy-isolated descriptor.
func (c CommandContext) Descriptor() metadata.Command { return cloneDescriptor(c.descriptor) }

// Command returns the decoded command receiver.
func (c CommandContext) Command() any { return c.command }

// CorrelationID returns the inherited or generated correlation.
func (c CommandContext) CorrelationID() correlation.ID { return c.correlation }

// ReceivedAt returns this frame's distinct UTC receipt.
func (c CommandContext) ReceivedAt() time.Time { return c.received }

// Principal returns immutable identity metadata.
func (c CommandContext) Principal() identity.Principal { return c.principal }

// Tenant returns the selected tenant, not evidence of membership.
func (c CommandContext) Tenant() tenancy.ID { return c.tenant }

// Values returns copied case-insensitive membership.
func (c CommandContext) Values() ContextValues { return ContextValues{entries: c.values.Entries()} }

// AllowedSeverity returns the explicit caller allowance, if supplied.
func (c CommandContext) AllowedSeverity() (validation.Severity, bool) {
	if c.allowed == nil {
		return 0, false
	}
	return *c.allowed, true
}

// IsValidationOnly distinguishes advisory validation from execution.
func (c CommandContext) IsValidationOnly() bool { return c.validationOnly }

// Response returns the selected response and its presence.
func (c CommandContext) Response() (any, bool) { return c.response, c.hasResponse }

// ResolvedKey preserves an authoritative empty key's presence.
func (c CommandContext) ResolvedKey() (string, bool) {
	value, ok := c.values.Get(ResolvedKey)
	if !ok {
		return "", false
	}
	key, ok := value.(string)
	return key, ok
}

type commandContextKey struct{}

// ContextFrom reads the snapshot installed for the current application callback.
func ContextFrom(ctx context.Context) (CommandContext, bool) {
	if ctx == nil {
		return CommandContext{}, false
	}
	value, ok := ctx.Value(commandContextKey{}).(CommandContext)
	return value, ok
}

// Invocation is an expiring callback frame, not a context-stored service locator.
// Accessors return nil facilities after expiry. Retained executors reject work.
type Invocation struct {
	mu       sync.Mutex
	active   bool
	snapshot CommandContext
	scope    *execution.Scope
	bound    *boundPipeline
	owner    *Execution
}

// CommandContext returns a copied snapshot including values set during this callback.
func (i *Invocation) CommandContext() CommandContext {
	if i == nil {
		return CommandContext{}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	value := i.snapshot
	value.values = value.Values()
	return value
}

// Scope returns a non-closing callback-scoped operation view, or nil after expiry.
func (i *Invocation) Scope() *execution.Scope {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active {
		return nil
	}
	return i.scope
}

// Pipeline returns an executor joining this command owner. Retained use fails.
func (i *Invocation) Pipeline() Pipeline {
	if i == nil {
		return nil
	}
	return i.bound
}

// Execution returns the current owner's callback view, or nil after expiry.
func (i *Invocation) Execution() *Execution {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active {
		return nil
	}
	return i.owner
}

// SetValue updates copied membership only; it cannot replace security or ownership.
func (i *Invocation) SetValue(name string, value any) error {
	if i == nil {
		return ErrNoContext
	}
	if !validExtensionName(name) {
		return ErrInvalidRegistration
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active {
		return ErrExecutionClosed
	}
	i.snapshot.values.entries[strings.ToLower(name)] = value
	return nil
}
