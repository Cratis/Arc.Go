// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/cratis/arc.go/internal/modelshape"
)

// RuleDescriptor is the portable, serializable server/client rule contract.
// Arguments are JSON string/number scalars, validated against Name before use.
// Nil Severity means Error. ServerOnly rules must supply their own server
// validator; neither the portable executor nor generator silently skips them.
type RuleDescriptor struct {
	Property   string            `json:"property,omitempty"`
	Name       string            `json:"name"`
	Arguments  []json.RawMessage `json:"arguments,omitempty"`
	Message    string            `json:"message,omitempty"`
	Severity   *Severity         `json:"severity,omitempty"`
	Source     string            `json:"source,omitempty"`
	ServerOnly bool              `json:"serverOnly,omitempty"`
	Optional   bool              `json:"optional,omitempty"`
	Concept    bool              `json:"concept,omitempty"`
}

// ParseRules reads the rules field tag's JSON array without executing validators.
// kind is string, number, boolean, collection or object. Paths are wire names.
// Explicit messages are required so both projections preserve server messages.
// Unsupported rules/regex/severity are construction errors, not weaker checks.
func ParseRules(text, property, kind string) ([]RuleDescriptor, error) {
	if text == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var rules []RuleDescriptor
	if err := decoder.Decode(&rules); err != nil {
		return nil, fmt.Errorf("%w: rules for %s: %v", ErrInvalidRegistration, property, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing rule input", ErrInvalidRegistration)
	}
	for i := range rules {
		rule := &rules[i]
		if rule.Property != "" && rule.Property != property {
			return nil, fmt.Errorf("%w: rule property mismatch", ErrInvalidRegistration)
		}
		rule.Property = property
		if rule.Source == "" {
			rule.Source = "explicit"
		}
		if rule.ServerOnly || rule.Message == "" || rule.Severity != nil && !validSeverity(*rule.Severity) {
			return nil, fmt.Errorf("%w: rule %s requires a static portable contract and message", ErrInvalidRegistration, rule.Name)
		}
		count := 0
		switch rule.Name {
		case "notNull", "notEmpty":
		case "minLength", "maxLength":
			count = 1
			if kind != "string" {
				return nil, fmt.Errorf("%w: string rule on %s", ErrInvalidRegistration, kind)
			}
		case "length":
			count = 2
			if kind != "string" {
				return nil, fmt.Errorf("%w: string rule on %s", ErrInvalidRegistration, kind)
			}
		case "greaterThan", "greaterThanOrEqual", "lessThan", "lessThanOrEqual":
			count = 1
			if kind != "number" {
				return nil, fmt.Errorf("%w: numeric rule on %s", ErrInvalidRegistration, kind)
			}
		case "emailAddress", "phone", "url":
			if kind != "string" {
				return nil, fmt.Errorf("%w: format rule on %s", ErrInvalidRegistration, kind)
			}
		case "matches":
			count = 1
			if kind != "string" {
				return nil, fmt.Errorf("%w: regex rule on %s", ErrInvalidRegistration, kind)
			}
		default:
			return nil, fmt.Errorf("%w: unsupported portable rule %q", ErrInvalidRegistration, rule.Name)
		}
		if len(rule.Arguments) != count {
			return nil, fmt.Errorf("%w: %s requires %d arguments", ErrInvalidRegistration, rule.Name, count)
		}
		for _, argument := range rule.Arguments {
			if rule.Name == "matches" {
				var pattern string
				if json.Unmarshal(argument, &pattern) != nil || pattern == "" || strings.ContainsAny(pattern, "\\^$().|\n\r:") || strings.Contains(pattern, "[[") || !ascii(pattern) {
					return nil, fmt.Errorf("%w: regex requires the portable ASCII literal/class/quantifier subset", ErrInvalidRegistration)
				}
				if _, err := regexp.Compile(pattern); err != nil {
					return nil, fmt.Errorf("%w: invalid regex", ErrInvalidRegistration)
				}
			} else {
				var number float64
				if bytes.Equal(argument, []byte("null")) || json.Unmarshal(argument, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > 9007199254740991 {
					return nil, fmt.Errorf("%w: argument requires a safe finite number", ErrInvalidRegistration)
				}
				if strings.Contains(rule.Name, "Length") || rule.Name == "length" {
					if number < 0 || number != math.Trunc(number) {
						return nil, fmt.Errorf("%w: length requires a nonnegative integer", ErrInvalidRegistration)
					}
				}
			}
		}
		if rule.Name == "length" && ruleNumber(*rule, 0) > ruleNumber(*rule, 1) {
			return nil, fmt.Errorf("%w: inverted length range", ErrInvalidRegistration)
		}
	}
	return rules, nil
}

func ascii(text string) bool {
	for _, c := range text {
		if c > 127 {
			return false
		}
	}
	return true
}
func ruleNumber(rule RuleDescriptor, index int) float64 {
	value, _ := strconv.ParseFloat(string(rule.Arguments[index]), 64)
	return value
}
func jsSpace(c rune) bool {
	return strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", c)
}

// Portable is an immutable concurrently callable validator built from field tags.
// Handwritten validators and concept inference remain separate server contracts.
type Portable[T any] struct{ rules []portableRule }
type portableRule struct {
	descriptor RuleDescriptor
	index      []int
}

// NewPortable compiles the explicitly declared rules tags on T's wire fields.
// It does not run constructors, application validators or codecs. Nullable
// values skip non-presence rules, matching the pinned JavaScript rules.
func NewPortable[T any]() (*Portable[T], error) {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields, err := modelshape.Fields(t)
	if err != nil {
		return nil, err
	}
	validator := &Portable[T]{}
	for _, field := range fields {
		base := field.Type
		for base.Kind() == reflect.Pointer {
			base = base.Elem()
		}
		kind := "object"
		switch base.Kind() {
		case reflect.String:
			kind = "string"
		case reflect.Bool:
			kind = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			kind = "number"
		case reflect.Slice, reflect.Array:
			kind = "collection"
		}
		rules, err := ParseRules(field.Tag.Get("rules"), field.Name, kind)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			validator.rules = append(validator.rules, portableRule{rule, field.Index})
		}
	}
	return validator, nil
}

// Validate evaluates compiled rules without mutating or retaining the model.
func (p *Portable[T]) Validate(ctx context.Context, model T) ([]Result, error) {
	if p == nil {
		return nil, ErrInvalidValidator
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := reflect.ValueOf(model)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	var results []Result
	for _, rule := range p.rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if portableValid(rule.descriptor, modelshape.Value(value, rule.index)) {
			continue
		}
		severity := Error
		if rule.descriptor.Severity != nil {
			severity = *rule.descriptor.Severity
		}
		results = append(results, Result{Severity: severity, Message: rule.descriptor.Message, Members: []string{rule.descriptor.Property}, Reason: Rule})
	}
	return results, nil
}

func portableValid(rule RuleDescriptor, value reflect.Value) bool {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			value = reflect.Value{}
			break
		}
		value = value.Elem()
	}
	present := value.IsValid() && ((value.Kind() != reflect.Slice && value.Kind() != reflect.Map) || !value.IsNil())
	if rule.Name == "notNull" {
		return present
	}
	if rule.Name == "notEmpty" {
		if !present {
			return false
		}
		if value.Kind() == reflect.String {
			return strings.TrimFunc(value.String(), jsSpace) != ""
		}
		if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			return value.Len() > 0
		}
		return true
	}
	if !present {
		return true
	}
	if value.Kind() == reflect.String {
		text := value.String()
		length := float64(len(utf16.Encode([]rune(text))))
		switch rule.Name {
		case "minLength":
			return length >= ruleNumber(rule, 0)
		case "maxLength":
			return length <= ruleNumber(rule, 0)
		case "length":
			return length >= ruleNumber(rule, 0) && length <= ruleNumber(rule, 1)
		case "matches":
			var pattern string
			_ = json.Unmarshal(rule.Arguments[0], &pattern)
			return text == "" || regexp.MustCompile(pattern).MatchString(text)
		case "phone":
			if text == "" {
				return true
			}
			for _, c := range text {
				if (c < '0' || c > '9') && !jsSpace(c) && !strings.ContainsRune("()+-", c) {
					return false
				}
			}
			return true
		case "url":
			if text == "" {
				return true
			}
			lower := strings.ToLower(text)
			start := 0
			if strings.HasPrefix(lower, "http://") {
				start = 7
			} else if strings.HasPrefix(lower, "https://") {
				start = 8
			}
			return start > 0 && len(text) > start && !strings.ContainsRune("\n\r\u2028\u2029", []rune(text[start:])[0])
		case "emailAddress":
			if text == "" {
				return true
			}
			for _, c := range text {
				if jsSpace(c) {
					return false
				}
			}
			parts := strings.Split(text, "@")
			if len(parts) != 2 || parts[0] == "" || len(parts[1]) < 3 {
				return false
			}
			return strings.Contains(parts[1][1:len(parts[1])-1], ".")
		}
	}
	var number float64
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number = float64(value.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number = float64(value.Uint())
	case reflect.Float32, reflect.Float64:
		number = value.Float()
	default:
		return false
	}
	threshold := ruleNumber(rule, 0)
	switch rule.Name {
	case "greaterThan":
		return number > threshold
	case "greaterThanOrEqual":
		return number >= threshold
	case "lessThan":
		return number < threshold
	case "lessThanOrEqual":
		return number <= threshold
	}
	return false
}
