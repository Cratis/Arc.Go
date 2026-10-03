// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func correctionsGraph(t *testing.T, profile ApplicationProfile) *Graph {
	t.Helper()
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, "testdata/wire-contract/corrections.go")))
	graph, err := buildGraph(graphPackages(t, dir, "."), profile, false)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestContractKnownTemporalCodecsOverridePrimitiveBacking(t *testing.T) {
	graph := correctionsGraph(t, contractProfile())
	fields := fieldsByName(graph.Commands[0].Fields)
	for _, tc := range []struct{ field, kind, format string }{
		{"span", "TimeSpan", "dotnet-time-span"}, {"date", "DateOnly", "date"}, {"clock", "TimeOnly", "local-time"},
	} {
		wire := fields[tc.field].Type
		scalar := wire.Contract.Scalar
		if wire.Kind != tc.kind || scalar == nil || scalar.Representation != "string" || scalar.Format != tc.format || scalar.Bits != 0 || scalar.Minimum != "" || scalar.Maximum != "" {
			t.Fatalf("%s: %+v", tc.field, wire)
		}
		if wire.Contract.InputNull || wire.Contract.OutputNull || !fields[tc.field].Presence.OutputRequired {
			t.Fatal(tc.field, fields[tc.field])
		}
	}
}

func TestContractPropertyOmissionPreservesInnerNull(t *testing.T) {
	graph := correctionsGraph(t, contractProfile())
	var fields map[string]FieldDescriptor
	for _, node := range graph.Types {
		if node.Key == "example.test/consumer.Presence" {
			fields = fieldsByName(node.Fields)
		}
	}
	if len(fields) != 4 {
		t.Fatal("missing presence fields", fields)
	}
	for _, tc := range []struct {
		field       string
		depth       int
		omitMissing bool
		outputNull  bool
	}{
		{"pointer", 1, false, false}, {"optional", 1, true, true}, {"inner", 2, false, true}, {"nested", 2, false, true},
	} {
		field := fields[tc.field]
		presence, wire := field.Presence, field.Type.Contract
		if presence.InputRequired || presence.InputMissing != "zero" || !presence.InputNull || presence.OutputRequired || !presence.OmitNil || presence.OmitMissing != tc.omitMissing || presence.OutputNull != tc.outputNull || presence.OmitEmpty || presence.OmitZero {
			t.Fatalf("%s property: %+v", tc.field, presence)
		}
		if wire.PointerDepth != tc.depth || !wire.InputNull || !wire.OutputNull {
			t.Fatalf("%s value: %+v", tc.field, wire)
		}
	}
}

func TestContractFrameworkStateRetainsResolvedWire(t *testing.T) {
	for _, tc := range []struct {
		name, kind, declared, minimum, maximum string
		bits                                   int
		unsigned, nullable                     bool
	}{
		{"State", "number", "example.test/consumer.State", "0", "18446744073709551615", 64, true, false},
		{"SignedState", "number", "example.test/consumer.SignedState", "-32768", "32767", 16, false, false},
		{"NullableState", "number", "example.test/consumer.NullableState", "0", "18446744073709551615", 64, true, true},
		{"NullableCodec", "declared", "example.test/consumer.NullableCodec", "", "", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := contractProfile()
			profile.ResponseFields["Cratis.ValidationResult"]["state"] = ResponseField{Type: "example.test/consumer." + tc.name}
			if tc.kind == "declared" {
				profile.WireSchemas = map[string]WireSchemas{"example.test/consumer.NullableCodec": {Input: json.RawMessage(`{"type":["object","null"]}`), Output: json.RawMessage(`{"type":"null"}`)}}
			}
			graph := correctionsGraph(t, profile)
			// Round-trip the compiler-free projection; target strings alone are
			// insufficient for named scalars with no Graph.Types node.
			data, err := json.Marshal(graph)
			if err != nil {
				t.Fatal(err)
			}
			var projection Graph
			if err := json.Unmarshal(data, &projection); err != nil {
				t.Fatal(err)
			}
			var field FieldDescriptor
			for _, framework := range projection.Framework {
				if framework.Key == "Cratis.ValidationResult" {
					field = fieldsByName(framework.Fields)["state"]
				}
			}
			wire := field.Type
			if wire.Kind != tc.kind || wire.Contract == nil || wire.Contract.Declared != tc.declared || wire.Contract.InputNull != tc.nullable || wire.Contract.OutputNull != tc.nullable {
				t.Fatalf("resolved state: %+v", wire)
			}
			if tc.bits > 0 {
				scalar := wire.Contract.Scalar
				if scalar == nil || scalar.Representation != "integer" || scalar.Bits != tc.bits || scalar.Unsigned != tc.unsigned || scalar.Minimum != tc.minimum || scalar.Maximum != tc.maximum {
					t.Fatalf("state scalar: %+v", scalar)
				}
			} else if wire.Contract.Schemas == nil {
				t.Fatal("state schema lost", wire)
			}
			if field.Presence == nil || field.Presence.OutputRequired || field.Presence.OutputNull || !field.Presence.OmitNull {
				t.Fatal("state envelope must omit encoded null", field)
			}
		})
	}
}

func TestContractCorrectionsCompiledWireWitness(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, "testdata/wire-contract/corrections.go")))
	put(t, filepath.Join(dir, "input_test.go"), string(get(t, "testdata/wire-contract/corrections_test.go")))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compiled wire witnesses: %v\n%s", err, output)
	}
}
