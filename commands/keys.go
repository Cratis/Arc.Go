// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"encoding"
	"fmt"
	"reflect"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// ContextValuesProvider supplies trusted preauthorization metadata. Later providers
// overwrite earlier membership. Factories must be cheap and honor cancellation.
type ContextValuesProvider interface {
	Provide(context.Context, *Invocation) (ContextValues, error)
}

// KeyProvider explicitly supplies a provider-neutral command key.
type KeyProvider interface{ GetKey() string }

// KeyResolver supplies trusted keys before filters; empty keys do not win.
type KeyResolver interface {
	Resolve(context.Context, *Invocation) (string, bool, error)
}

// AddContextValuesProvider registers an ordered, lazy metadata provider.
func (r *Registry) AddContextValuesProvider(name string, factory Factory[ContextValuesProvider], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	return addExtension(r, "values", name, factory, keys, &r.providers)
}

// AddKeyResolver registers a custom resolver before the command's own fallback.
func (r *Registry) AddKeyResolver(name string, factory Factory[KeyResolver], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	return addExtension(r, "keys", name, factory, keys, &r.keys)
}
func keyText(value reflect.Value) (string, bool, error) {
	if !value.IsValid() || !value.CanInterface() || nilValue(value.Interface()) {
		return "", false, nil
	}
	if text, ok := value.Interface().(encoding.TextMarshaler); ok {
		bytes, err := text.MarshalText()
		if err != nil {
			return "", false, err
		}
		return string(bytes), len(bytes) != 0, nil
	}
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	text := fmt.Sprint(value.Interface())
	return text, text != "", nil
}
