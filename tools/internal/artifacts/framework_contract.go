// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import "fmt"

// FrameworkContract retains known output-only envelope structure, not an OpenAPI
// schema. Payload fields are supplied by operation descriptors, never implicit any.
type FrameworkContract struct {
	Key             string            `json:"key"`
	Source          string            `json:"source"`
	Fields          []FieldDescriptor `json:"fields"`
	Payload         string            `json:"payload,omitempty"`
	PayloadPresence string            `json:"payloadPresence,omitempty"`
}

func outputField(name string, wire WireType, required, null bool) FieldDescriptor {
	return FieldDescriptor{Name: name, Type: wire, Optional: !required, Presence: &FieldPresence{OutputRequired: required, OutputNull: null}}
}

func frameworkContracts(graph *Graph) ([]FrameworkContract, error) {
	stringWire := WireType{Kind: "string"}
	boolWire := WireType{Kind: "boolean"}
	stringsWire := WireType{Kind: "array", Element: &stringWire}
	findingsWire := WireType{Kind: "array", Element: &WireType{Kind: "framework", Target: "Cratis.ValidationResult"}}
	common := []FieldDescriptor{
		outputField("correlationId", WireType{Kind: "Guid"}, true, false),
		outputField("isSuccess", boolWire, true, false),
		outputField("isAuthorized", boolWire, true, false),
		outputField("isValid", boolWire, true, false),
		outputField("hasExceptions", boolWire, true, false),
		outputField("validationResults", findingsWire, true, false),
		outputField("exceptionMessages", stringsWire, true, false),
		outputField("exceptionStackTrace", stringWire, true, false),
	}
	command := FrameworkContract{Key: "Cratis.CommandResult", Source: "commands/result.go", Fields: append(append([]FieldDescriptor(nil), common...), outputField("authorizationFailureReason", stringWire, true, false)), Payload: "response", PayloadPresence: "success-and-present-nonnull"}
	query := FrameworkContract{Key: "Cratis.QueryResult", Source: "queries/result.go", Fields: append(append([]FieldDescriptor(nil), common...), outputField("isReady", boolWire, true, false), outputField("paging", WireType{Kind: "framework", Target: "Cratis.PagingInfo"}, true, false)), Payload: "data", PayloadPresence: "present-nonnull-independent-of-readiness"}
	validation := FrameworkContract{Key: "Cratis.ValidationResult", Source: "validation/result.go", Fields: []FieldDescriptor{
		outputField("severity", WireType{Kind: "number", Contract: &WireContract{Scalar: &ScalarContract{GoKind: "int32", Representation: "integer", Bits: 32, Minimum: "-2147483648", Maximum: "2147483647"}}}, true, false),
		outputField("message", stringWire, true, false), outputField("members", stringsWire, true, false), outputField("reason", stringWire, true, false), outputField("reasonDetail", stringWire, false, false),
	}}
	state, exists := graph.Profile.ResponseFields["Cratis.ValidationResult"]["state"]
	if !exists {
		return nil, fmt.Errorf("Cratis.ValidationResult.state requires explicit responseFields absent/type/schema assertion; no implicit any")
	}
	if !state.Absent {
		wire := WireType{Kind: "declared"}
		if state.Type != "" {
			wire.Target = state.Type
		} else {
			wire.Contract = &WireContract{Schemas: &WireSchemas{Input: state.Schema, Output: state.Schema}}
		}
		validation.Fields = append(validation.Fields, outputField("state", wire, false, false))
	}
	integer := WireType{Kind: "number", Contract: &WireContract{Scalar: &ScalarContract{GoKind: "int64", Representation: "integer", Bits: 64, Minimum: "-9223372036854775808", Maximum: "9223372036854775807"}}}
	paging := FrameworkContract{Key: "Cratis.PagingInfo", Source: "queries/paging.go"}
	for _, name := range []string{"page", "size", "totalItems", "totalPages"} {
		wire := integer
		if name != "totalItems" {
			wire = WireType{Kind: "number", Contract: &WireContract{Scalar: &ScalarContract{GoKind: "int32", Representation: "integer", Bits: 32, Minimum: "-2147483648", Maximum: "2147483647"}}}
		}
		paging.Fields = append(paging.Fields, outputField(name, wire, true, false))
	}
	validate := command
	validate.Key = "Cratis.CommandValidationResult"
	validate.Payload = ""
	validate.PayloadPresence = ""
	contracts := []FrameworkContract{command, validate, query, validation, paging}
	if graph.Profile.OpenAPI.IncludeFrameworkEndpoints {
		details := WireType{Kind: "null"}
		if key := graph.Profile.Server.Identity.DetailsType; key != "" {
			details = WireType{Kind: "declared", Target: key}
		}
		identity := FrameworkContract{Key: "Cratis.Identity", Source: "identity/view.go", Fields: []FieldDescriptor{outputField("id", stringWire, true, false), outputField("name", stringWire, true, false), outputField("isAuthenticated", boolWire, true, false), outputField("isAuthorized", boolWire, true, false), outputField("roles", stringsWire, true, false), outputField("details", details, true, true)}}
		contracts = append(contracts, identity)
	}
	return contracts, nil
}
