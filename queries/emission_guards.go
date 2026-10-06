// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// EmissionVerdict controls delivery after authorization and interception.
type EmissionVerdict uint8

const (
	// Allow permits this candidate, subject to later guards and continuity checks.
	Allow EmissionVerdict = iota
	// Suppress skips this candidate without advancing delivered state.
	Suppress
	// DenyAndTerminate ends the observation with an unauthorized result.
	DenyAndTerminate
)

// EmissionGuard revalidates subscription-specific delivery. Callbacks are synchronous.
// Errors, panics, and unknown verdicts fail closed; raw errors remain local.
type EmissionGuard interface {
	Guard(context.Context, EmissionContext) (EmissionVerdict, error)
}

// EmissionGuardFunc adapts a synchronous emission guard.
type EmissionGuardFunc func(context.Context, EmissionContext) (EmissionVerdict, error)

// Guard invokes the callback.
func (f EmissionGuardFunc) Guard(ctx context.Context, c EmissionContext) (EmissionVerdict, error) {
	return f(ctx, c)
}

// EmissionContext is an isolated per-guard snapshot, never a resource resolver.
// Arguments and SubscriptionScope return guard-local detached values.
type EmissionContext struct {
	query     QueryContext
	arguments any
	scope     any
	first     bool
}

// Name returns the fully qualified query identity.
func (c EmissionContext) Name() FullyQualifiedQueryName { return c.query.Name() }

// Arguments returns this guard's bound argument snapshot.
func (c EmissionContext) Arguments() any { return c.arguments }

// Principal returns the subscriber identity, not the producer's identity.
func (c EmissionContext) Principal() identity.Principal { return c.query.Principal() }

// Tenant returns the subscriber tenant.
func (c EmissionContext) Tenant() tenancy.ID { return c.query.Tenant() }

// CorrelationID remains stable across emissions.
func (c EmissionContext) CorrelationID() correlation.ID { return c.query.CorrelationID() }

// QueryContext returns receipt and immutable query metadata.
func (c EmissionContext) QueryContext() QueryContext { return c.query }

// FirstDelivered reports that no candidate has yet been successfully delivered.
func (c EmissionContext) FirstDelivered() bool { return c.first }

// SubscriptionScope returns filter-supplied guard-local scope data, not execution resources.
func (c EmissionContext) SubscriptionScope() any { return c.scope }

type guardEntry struct {
	name    string
	factory Factory[EmissionGuard]
	keys    []di.Key
}

// AddEmissionGuard appends a named lazy guard factory in declaration order.
// Factories activate only for authorized observable emissions, not unary queries.
func (r *Registry) AddEmissionGuard(name string, f Factory[EmissionGuard], keys ...di.Key) error {
	if r == nil || name == "" || f == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	for _, entry := range r.guards {
		if entry.name == name {
			return ErrDuplicate
		}
	}
	r.guards = append(r.guards, guardEntry{name: name, factory: f, keys: slices.Clone(keys)})
	return nil
}

// frozenValue retains immutable JSON and the exact type, never live provider pointers.
// Unsupported JSON values, cycles, and incompatible custom decoders fail closed.
type frozenValue struct {
	typ   reflect.Type
	bytes []byte
}

func freezeValue(value any) (frozenValue, error) {
	if value == nil {
		return frozenValue{}, nil
	}
	if err := checkSnapshotType(reflect.TypeOf(value), map[reflect.Type]bool{}); err != nil {
		return frozenValue{}, err
	}
	if err := checkSnapshotValue(reflect.ValueOf(value), 0); err != nil {
		return frozenValue{}, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return frozenValue{}, err
	}
	f := frozenValue{typ: reflect.TypeOf(value), bytes: data}
	// Check reconstruction before accepting any part of the snapshot.
	_, err = f.thaw()
	return f, err
}
func checkSnapshotType(t reflect.Type, seen map[reflect.Type]bool) error {
	if seen[t] {
		return nil
	}
	seen[t] = true
	if t.Implements(reflect.TypeFor[json.Marshaler]()) && reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return nil
	}
	if _, ok := t.MethodByName("Open"); ok {
		return ErrResponseType
	}
	if _, ok := t.MethodByName("Next"); ok {
		return ErrResponseType
	}
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return ErrResponseType
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return checkSnapshotType(t.Elem(), seen)
	case reflect.Map:
		if err := checkSnapshotType(t.Key(), seen); err != nil {
			return err
		}
		return checkSnapshotType(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Name == "_" {
				continue
			}
			if field.PkgPath != "" {
				return ErrResponseType
			}
			if err := checkSnapshotType(field.Type, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkSnapshotValue(v reflect.Value, depth int) error {
	if !v.IsValid() {
		return nil
	}
	if depth > 128 {
		return ErrResponseType
	}
	if err := checkSnapshotType(v.Type(), map[reflect.Type]bool{}); err != nil {
		return err
	}
	if v.Type().Implements(reflect.TypeFor[json.Marshaler]()) {
		return nil
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return checkSnapshotValue(v.Elem(), depth+1)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := checkSnapshotValue(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if err := checkSnapshotValue(it.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).Name == "_" {
				continue
			}
			if err := checkSnapshotValue(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f frozenValue) thaw() (any, error) {
	if f.typ == nil {
		return nil, nil
	}
	value := reflect.New(f.typ)
	if err := json.Unmarshal(f.bytes, value.Interface()); err != nil {
		return nil, err
	}
	return value.Elem().Interface(), nil
}
func detachValue(value any) (any, error) {
	f, err := freezeValue(value)
	if err != nil {
		return nil, err
	}
	return f.thaw()
}

type subscriptionScope struct {
	mu     sync.Mutex
	value  frozenValue
	frozen bool
}
type subscriptionScopeKey struct{}

func subscriptionScopeFrom(ctx context.Context) *subscriptionScope {
	s, _ := ctx.Value(subscriptionScopeKey{}).(*subscriptionScope)
	return s
}

// SetSubscriptionScope captures guard data during input filtering. Later filters
// may replace it. Values must round-trip through JSON; cycles, functions, channels,
// and streams are unsupported. After filtering, or outside an observable filter,
// this fails explicitly. A retained Invocation cannot set data after its callback.
func (i *Invocation) SetSubscriptionScope(ctx context.Context, value any) error {
	if i == nil || i.subscriptionScope == nil {
		return ErrInvalidRegistration
	}
	if err := i.scope.CheckContext(ctx); err != nil {
		return err
	}
	f, err := freezeValue(value)
	if err != nil {
		return err
	}
	s := i.subscriptionScope
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen {
		return execution.ErrScopeExpired
	}
	s.value = f
	return nil
}
