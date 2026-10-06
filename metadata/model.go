// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metadata

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/validation"
)

// ErrInvalidModel identifies malformed model declarations or supported field tags.
var ErrInvalidModel = errors.New("invalid model metadata")

// ModelKind identifies explicit model opt-in; empty leaves selection to registration.
type ModelKind string

const (
	// CommandModel identifies a command declaration.
	CommandModel ModelKind = "command"
	// ReadModel identifies a read-model declaration.
	ReadModel ModelKind = "readmodel"
)

// Model is declaration metadata shared by handwritten and generated registration.
// It owns its authorization slices and severity pointer. Explicit registration
// options override individual fields after InspectModel; validate the merged model.
type Model struct {
	// Kind is optional; registration supplies the expected artifact kind.
	Kind ModelKind
	// Type is the stable public identity, never derived from the import path.
	Type TypeName
	// Path is an optional literal absolute route override.
	Path string
	// Authorization is the optional explicit declaration.
	Authorization *Authorization
	// BlockOnValidationSeverity is an optional command validation floor.
	BlockOnValidationSeverity *validation.Severity
	// ExcludeFromDiscovery hides discovery metadata, not execution endpoints.
	ExcludeFromDiscovery bool
	// KeyMember names an explicitly tagged command key in wire form.
	KeyMember string
	// IdentityMember names an explicitly tagged read-model identity in wire form.
	IdentityMember string
}

// InspectModel reads one named struct (or its pointer), using namespace as the
// default and the Go type name as the default name. Source comments are not read.
// An optional single blank field supplies metadata without an embedded base type:
//
//	_ struct{} `json:"-" arc:"command,name=Add,path=/items/add,block-on=warning" authorize:"roles=Editor|Admin,policy=CanWrite"`
//
// arc supports command/readmodel, name, namespace, path, block-on, authorize,
// allow-anonymous and exclude-from-discovery. authorize on arc requires an actor;
// the separate authorize tag uses semicolon-separated AND requirements, each with
// roles (pipe-separated OR) and/or policy. Empty authorize means authentication.
// Comma-separated options escape comma, equals and backslash with backslash.
// Unknown/duplicate options and multiple blank declarations are errors. Other
// tag namespaces are untouched. Field arc tags support only key and identity.
func InspectModel(t reflect.Type, namespace string) (Model, error) {
	if t == nil {
		return Model{}, ErrInvalidModel
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return Model{}, ErrInvalidModel
	}
	model := Model{Type: TypeName{Namespace: namespace, Name: t.Name()}}
	declared := false
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		arc, hasArc := field.Tag.Lookup("arc")
		auth, hasAuth := field.Tag.Lookup("authorize")
		if field.Name != "_" {
			if hasAuth {
				return Model{}, fmt.Errorf("%w: authorization belongs on a blank declaration field", ErrInvalidModel)
			}
			base := field.Type
			if base.Kind() == reflect.Pointer {
				base = base.Elem()
			}
			if hasArc && field.Anonymous && base.Kind() == reflect.Struct && strings.Split(field.Tag.Get("json"), ",")[0] == "" {
				return Model{}, fmt.Errorf("%w: tagged embedded members need an explicit JSON name", ErrInvalidModel)
			}
			if hasArc && (!field.IsExported() || strings.Split(field.Tag.Get("json"), ",")[0] == "-") {
				return Model{}, fmt.Errorf("%w: arc field tags require readable members", ErrInvalidModel)
			}
			continue
		}
		if !hasArc && !hasAuth {
			continue
		}
		if declared || field.Tag.Get("json") != "-" {
			return Model{}, ErrInvalidModel
		}
		declared = true
		if err := applyDeclaration(&model, arc); err != nil {
			return Model{}, err
		}
		if hasAuth {
			if model.Authorization != nil {
				return Model{}, fmt.Errorf("%w: conflicting authorization tags", ErrInvalidModel)
			}
			var err error
			model.Authorization, err = parseAuthorization(auth)
			if err != nil {
				return Model{}, err
			}
		}
	}
	fields, err := modelshape.Fields(t)
	if err != nil {
		return Model{}, fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}
	for _, field := range fields {
		options, err := modelshape.Options(field.Tag.Get("arc"))
		if err != nil {
			return Model{}, fmt.Errorf("%w: %v", ErrInvalidModel, err)
		}
		for _, option := range options {
			if option.HasValue {
				return Model{}, ErrInvalidModel
			}
			switch option.Name {
			case "key":
				if model.KeyMember != "" {
					return Model{}, fmt.Errorf("%w: multiple command keys", ErrInvalidModel)
				}
				model.KeyMember = field.Name
			case "identity":
				if model.IdentityMember != "" {
					return Model{}, fmt.Errorf("%w: multiple model identities", ErrInvalidModel)
				}
				model.IdentityMember = field.Name
			default:
				return Model{}, fmt.Errorf("%w: unknown arc field option %q", ErrInvalidModel, option.Name)
			}
		}
		if _, ok := field.Tag.Lookup("authorize"); ok {
			return Model{}, fmt.Errorf("%w: authorization belongs on a blank declaration field", ErrInvalidModel)
		}
		if _, err := validation.ParseTags(field.Tag.Get("validate")); err != nil {
			return Model{}, errors.Join(ErrInvalidModel, err)
		}
		if _, err := ParseQueryTags(field.Tag.Get("query")); err != nil {
			return Model{}, err
		}
	}
	return model, model.Validate()
}

// Validate checks the same declaration rules for generated and manual metadata.
// Policy existence is checked later by authorization.Registry.Build.
func (m Model) Validate() error {
	if m.Kind != "" && m.Kind != CommandModel && m.Kind != ReadModel {
		return ErrInvalidModel
	}
	if err := validateType(m.Type); err != nil {
		return errors.Join(ErrInvalidModel, err)
	}
	if m.Path != "" {
		if err := validatePath(m.Path); err != nil {
			return errors.Join(ErrInvalidModel, err)
		}
	}
	if m.BlockOnValidationSeverity != nil {
		if m.Kind == ReadModel || *m.BlockOnValidationSeverity < validation.Unknown || *m.BlockOnValidationSeverity > validation.Error {
			return ErrInvalidModel
		}
	}
	if auth := m.Authorization; auth != nil {
		if auth.AllowAnonymous && len(auth.Requirements) > 0 {
			return ErrInvalidModel
		}
		for _, requirement := range auth.Requirements {
			if len(requirement.AuthenticationSchemes) > 0 {
				return fmt.Errorf("%w: authentication schemes unsupported", ErrInvalidModel)
			}
			if requirement.Policy != "" && !validMetadataName(requirement.Policy) {
				return ErrInvalidModel
			}
			for _, role := range requirement.Roles {
				if !validMetadataName(role) {
					return ErrInvalidModel
				}
			}
		}
	}
	return nil
}

func applyDeclaration(model *Model, text string) error {
	options, err := modelshape.Options(text)
	if err != nil {
		return errors.Join(ErrInvalidModel, err)
	}
	for _, option := range options {
		switch option.Name {
		case "name", "namespace", "path", "block-on":
			if !option.HasValue {
				return ErrInvalidModel
			}
			switch option.Name {
			case "name":
				model.Type.Name = option.Value
			case "namespace":
				model.Type.Namespace = option.Value
			case "path":
				model.Path = option.Value
			case "block-on":
				severity, ok := map[string]validation.Severity{"unknown": validation.Unknown, "information": validation.Information, "warning": validation.Warning, "error": validation.Error}[option.Value]
				if !ok {
					return ErrInvalidModel
				}
				model.BlockOnValidationSeverity = &severity
			}
		case "command", "readmodel":
			if option.HasValue || model.Kind != "" {
				return ErrInvalidModel
			}
			model.Kind = ModelKind(option.Name)
		case "authorize", "allow-anonymous":
			if option.HasValue || model.Authorization != nil {
				return ErrInvalidModel
			}
			model.Authorization = &Authorization{AllowAnonymous: option.Name == "allow-anonymous"}
		case "exclude-from-discovery":
			if option.HasValue {
				return ErrInvalidModel
			}
			model.ExcludeFromDiscovery = true
		default:
			return fmt.Errorf("%w: unknown arc declaration option %q", ErrInvalidModel, option.Name)
		}
	}
	return nil
}

func parseAuthorization(text string) (*Authorization, error) {
	auth := &Authorization{}
	if text == "" {
		return auth, nil
	}
	for _, group := range strings.Split(text, ";") {
		if group == "" {
			return nil, ErrInvalidModel
		}
		options, err := modelshape.Options(group)
		if err != nil {
			return nil, errors.Join(ErrInvalidModel, err)
		}
		requirement := AuthorizationRequirement{}
		for _, option := range options {
			if !option.HasValue || option.Value == "" {
				return nil, ErrInvalidModel
			}
			switch option.Name {
			case "roles":
				requirement.Roles = strings.Split(option.Value, "|")
			case "policy":
				requirement.Policy = option.Value
			default:
				return nil, fmt.Errorf("%w: unsupported authorization option %q", ErrInvalidModel, option.Name)
			}
		}
		auth.Requirements = append(auth.Requirements, requirement)
	}
	return auth, nil
}

func validMetadataName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// QueryTags describes explicit query input options. Scalar conversion and
// Optional compatibility are validated by query registration, not this parser.
type QueryTags struct {
	// Required distinguishes required input from a supplied zero value.
	Required bool
	// Default is raw scalar text; HasDefault distinguishes default= from absence.
	Default string
	// HasDefault reports an explicitly supplied default.
	HasDefault bool
	// PreservePresence retains missing/null/empty distinctions.
	PreservePresence bool
}

// ParseQueryTags accepts required, default=value and preservePresence. Options
// use comma separation and backslash escaping for comma, equals and backslash.
// Required/default is contradictory; a default may preserve raw presence while
// supplying a separate effective value.
func ParseQueryTags(text string) (QueryTags, error) {
	options, err := modelshape.Options(text)
	if err != nil {
		return QueryTags{}, errors.Join(ErrInvalidModel, err)
	}
	var tags QueryTags
	for _, option := range options {
		switch option.Name {
		case "required":
			if option.HasValue {
				return QueryTags{}, ErrInvalidModel
			}
			tags.Required = true
		case "preservePresence":
			if option.HasValue {
				return QueryTags{}, ErrInvalidModel
			}
			tags.PreservePresence = true
		case "default":
			if !option.HasValue {
				return QueryTags{}, ErrInvalidModel
			}
			tags.HasDefault, tags.Default = true, option.Value
		default:
			return QueryTags{}, fmt.Errorf("%w: unknown query option %q", ErrInvalidModel, option.Name)
		}
	}
	if tags.HasDefault && tags.Required {
		return QueryTags{}, ErrInvalidModel
	}
	return tags, nil
}
