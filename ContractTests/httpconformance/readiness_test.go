// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"fmt"
	"slices"
	"testing"
)

type performerReadiness struct {
	Name               string `json:"name"`
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Path               string `json:"path"`
}
type endpointReadiness struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Method string `json:"method"`
}
type fixtureActivation struct {
	Performers []performerReadiness `json:"performers"`
	Endpoints  []endpointReadiness  `json:"endpoints"`
}

func expectedActivation() fixtureActivation {
	var result fixtureActivation
	for _, query := range []struct{ name, path string }{{"Plain", "/api/plain"}, {"Renderable", "/api/renderable"}, {"Filter", "/api/filter"}, {"Failing", "/api/failing"}} {
		name := "HttpConformance.Row." + query.name
		result.Performers = append(result.Performers, performerReadiness{query.name, name, query.path})
		result.Endpoints = append(result.Endpoints, endpointReadiness{"Execute" + name, query.path, "GET"}, endpointReadiness{"Query" + name, query.path, "QUERY"})
	}
	return result
}

func validateActivation(got *fixtureActivation) error {
	want := expectedActivation()
	if got == nil || len(got.Performers) != 4 || len(got.Endpoints) != 8 {
		return fmt.Errorf("readiness requires exactly four Row performers and eight GET/QUERY endpoints")
	}
	for _, performer := range want.Performers {
		count := 0
		for _, actual := range got.Performers {
			if actual == performer {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("missing, duplicate or wrong readiness performer %+v", performer)
		}
	}
	for _, endpoint := range want.Endpoints {
		count := 0
		for _, actual := range got.Endpoints {
			if actual == endpoint {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("missing, duplicate or wrong readiness endpoint %+v", endpoint)
		}
	}
	return nil
}

func TestActivationReadinessRejectsIncompleteDuplicateAndWrongMappings(t *testing.T) {
	valid := expectedActivation()
	if err := validateActivation(&valid); err != nil {
		t.Fatal(err)
	}
	for _, defect := range []string{"absent", "missing-performer", "missing-query", "duplicate-performer", "duplicate-endpoint", "wrong-path", "wrong-method", "wrong-name"} {
		t.Run(defect, func(t *testing.T) {
			got := fixtureActivation{slices.Clone(valid.Performers), slices.Clone(valid.Endpoints)}
			switch defect {
			case "absent":
				if validateActivation(nil) == nil {
					t.Fatal("absent activation admitted")
				}
				return
			case "missing-performer":
				got.Performers = got.Performers[1:]
			case "missing-query":
				got.Endpoints = append(got.Endpoints[:1], got.Endpoints[2:]...)
			case "duplicate-performer":
				got.Performers[0] = got.Performers[1]
			case "duplicate-endpoint":
				got.Endpoints[0] = got.Endpoints[1]
			case "wrong-path":
				got.Endpoints[0].Path = "/api/wrong"
			case "wrong-method":
				got.Endpoints[1].Method = "GET"
			case "wrong-name":
				got.Endpoints[0].Name = "ExecuteWrong.Row.Plain"
			}
			if validateActivation(&got) == nil {
				t.Fatal("invalid activation admitted")
			}
		})
	}
}
