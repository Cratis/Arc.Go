// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"io"
	"os"
	"strings"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Service configuration deliberately lives outside the wire/application profile.
type serviceBindingsConfig struct {
	FormatVersion          int                      `json:"formatVersion"`
	Package                string                   `json:"package"`
	Constructors           []string                 `json:"constructors,omitempty"`
	MatchIFoo              bool                     `json:"matchIFoo,omitempty"`
	Interfaces             []serviceInterfaceConfig `json:"interfaces,omitempty"`
	Existing               []serviceExistingConfig  `json:"existing,omitempty"`
	Duplicates             string                   `json:"duplicates,omitempty"`
	RequireAllDependencies bool                     `json:"requireAllDependencies,omitempty"`
}
type serviceInterfaceConfig struct {
	Service        string `json:"service"`
	Implementation string `json:"implementation"`
}
type serviceExistingConfig struct {
	Key      string `json:"key"`
	Lifetime string `json:"lifetime"`
}

func readServiceBindingsConfig(path string) (*serviceBindingsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Reject duplicate object members as well as unknown fields and trailing data.
	scanner := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSONValue(scanner); err != nil {
		return nil, fmt.Errorf("bindings configuration: %w", err)
	}
	if _, err := scanner.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing bindings configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg serviceBindingsConfig
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("bindings configuration: %w", err)
	}
	if cfg.FormatVersion != 1 {
		return nil, fmt.Errorf("unsupported bindings configuration format %d", cfg.FormatVersion)
	}
	if cfg.Package == "" {
		return nil, fmt.Errorf("bindings configuration requires one explicit package import path")
	}
	if cfg.Duplicates != "" && cfg.Duplicates != "reject" && cfg.Duplicates != "keepExisting" {
		return nil, fmt.Errorf("unknown bindings duplicate policy %q", cfg.Duplicates)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, err
	}
	for name := range members {
		switch name {
		case "formatVersion", "package", "constructors", "matchIFoo", "interfaces", "existing", "duplicates", "requireAllDependencies":
		default:
			return nil, fmt.Errorf("unknown bindings configuration member %q", name)
		}
	}
	for _, field := range []string{"interfaces", "existing"} {
		if raw, present := members[field]; present {
			var entries []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &entries); err != nil {
				return nil, err
			}
			for _, entry := range entries {
				for name := range entry {
					valid := field == "interfaces" && (name == "service" || name == "implementation") || field == "existing" && (name == "key" || name == "lifetime")
					if !valid {
						return nil, fmt.Errorf("unknown bindings %s member %q", field, name)
					}
				}
			}
		}
	}
	if value, present := members["constructors"]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, fmt.Errorf("constructors must be omitted or an array, not null")
	}
	return &cfg, nil
}

func uniqueJSONValue(d *json.Decoder) error {
	value, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, compound := value.(json.Delim)
	if !compound {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delimiter == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON member %q", name)
			}
			seen[name] = true
		}
		if err := uniqueJSONValue(d); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

// References are exact local Name or import/path.Name, optionally prefixed '*'.
// Only selected packages and their direct compiler imports are addressable.
// Closed generic instantiations can be supplied by named aliases; no expressions,
// implicit pointers, fuzzy names, import loading or configuration execution exist.
type serviceReferences struct {
	owner    *types.Package
	packages map[string]*types.Package
}

func (r serviceReferences) object(reference string) (types.Object, error) {
	pkg, name := r.owner, reference
	if index := strings.LastIndex(reference, "."); index >= 0 {
		pkg, name = r.packages[reference[:index]], reference[index+1:]
	}
	if pkg == nil || !token.IsIdentifier(name) {
		return nil, fmt.Errorf("invalid or unavailable exact reference %q", reference)
	}
	object := pkg.Scope().Lookup(name)
	if object == nil {
		return nil, fmt.Errorf("exact reference %q not found", reference)
	}
	if pkg != r.owner && !object.Exported() {
		return nil, fmt.Errorf("inaccessible exact reference %q", reference)
	}
	return object, nil
}
func (r serviceReferences) typ(reference string) (types.Type, error) {
	name := strings.TrimPrefix(reference, "*")
	var object types.Object
	var err error
	if !strings.Contains(name, ".") {
		object = r.owner.Scope().Lookup(name)
		if object == nil {
			object = types.Universe.Lookup(name)
		}
	}
	if object == nil {
		object, err = r.object(name)
	}
	if err != nil {
		return nil, err
	}
	typeName, ok := object.(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("reference %q is not a type", reference)
	}
	result := typeName.Type()
	if name != reference {
		result = types.NewPointer(result)
	}
	return result, nil
}
func serviceLifetime(name string) (di.Lifetime, error) {
	switch name {
	case "singleton":
		return di.Singleton, nil
	case "scoped":
		return di.Scoped, nil
	case "transient":
		return di.Transient, nil
	default:
		return 0, fmt.Errorf("existing requires an actual lifetime attestation (singleton, scoped or transient), got %q", name)
	}
}
