// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/serialization"
)

// newActionRequest compiles only the admitted flat FromRequest profile. Body-only
// binding uses the existing serializer; no second general-purpose binder lives here.
func newActionRequest[T any](merge bool) (func([]byte, T) (T, error), error) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct || t.NumMethod() != 0 || reflect.PointerTo(t).NumMethod() != 0 {
		return nil, fmt.Errorf("action input must be a struct without custom methods: %w", ErrInvalidOptions)
	}
	if err := serialization.ValidateType(t); err != nil {
		return nil, err
	}
	fields, err := modelshape.Fields(t)
	if err != nil {
		return nil, err
	}
	if merge {
		for _, field := range fields {
			if len(field.Index) != 1 || !actionScalar(field.Type) {
				return nil, fmt.Errorf("action request member %s requires a flat scalar or scalar pointer: %w", field.Name, ErrInvalidOptions)
			}
		}
	}
	return func(body []byte, request T) (T, error) {
		var result T
		if len(body) == 0 {
			body = []byte("{}")
		}
		body = bytes.TrimSpace(body)
		if len(body) == 0 || body[0] != '{' {
			return result, fmt.Errorf("action body must be an object")
		}
		var members map[string]json.RawMessage
		if merge {
			var err error
			body, members, err = actionMergeBody(body, fields)
			if err != nil {
				return result, err
			}
		}
		if err := serialization.Unmarshal(body, &result); err != nil {
			return result, err
		}
		if !merge {
			return result, nil
		}
		// Clone caller-owned pointer fields, matching the C# binder's JSON clone.
		data, err := serialization.Marshal(request)
		if err != nil {
			return result, err
		}
		var source T
		if err := serialization.Unmarshal(data, &source); err != nil {
			return result, err
		}
		out, in := reflect.ValueOf(&result).Elem(), reflect.ValueOf(source)
		for _, field := range fields {
			target, value := out.Field(field.Index[0]), in.Field(field.Index[0])
			isDefault := target.IsZero()
			// C# string default is null, not "". A Go string needs the JSON
			// presence bit to preserve an explicitly supplied empty string.
			if field.Type.Kind() == reflect.String {
				raw, present := members[field.Name]
				isDefault = !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
			}
			if isDefault && !value.IsZero() {
				target.Set(value)
			}
		}
		return result, nil
	}, nil
}

// actionMergeBody retains the root serializer's exact wire-name and duplicate
// rules. Only a null plain string is translated to its Go zero for default-value
// merging; pointer strings retain actual null/empty presence without translation.
func actionMergeBody(body []byte, fields []modelshape.Field) ([]byte, map[string]json.RawMessage, error) {
	// Validate the original document, including depth in unknown fields, before
	// projecting known fields. Normalization must not bypass serializer limits.
	var complete json.RawMessage
	if err := serialization.Unmarshal(body, &complete); err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return nil, nil, err
	}
	members := make(map[string]json.RawMessage)
	normalized := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, nil, fmt.Errorf("invalid action member")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, nil, err
		}
		for _, field := range fields {
			if name != field.Name {
				continue
			}
			if _, duplicate := members[name]; duplicate {
				return nil, nil, &serialization.DuplicateMemberError{Member: name}
			}
			members[name], normalized[name] = raw, raw
			if field.Type.Kind() == reflect.String && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				normalized[name] = json.RawMessage(`""`)
			}
		}
	}
	normalizedBody, err := json.Marshal(normalized)
	return normalizedBody, members, err
}

func actionScalar(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	// Custom concepts/codecs need their own default-value contract, not an
	// accidental approximation based on their Go representation.
	if t.PkgPath() != "" || t.NumMethod() != 0 || reflect.PointerTo(t).NumMethod() != 0 {
		return false
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
