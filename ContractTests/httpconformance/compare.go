// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
)

type exchange struct {
	Status int         `json:"status"`
	Header http.Header `json:"headers"`
	Body   []byte      `json:"body"`
}

var relevantHeaders = []string{"Content-Type", "Cache-Control", "X-Correlation-ID", "Content-Encoding", "Vary", "Allow"}

func compare(a, b exchange) []string {
	var differences []string
	if a.Status != b.Status {
		differences = append(differences, fmt.Sprintf("status: C# %d; Go %d", a.Status, b.Status))
	}
	for _, name := range relevantHeaders {
		if !reflect.DeepEqual(a.Header.Values(name), b.Header.Values(name)) {
			differences = append(differences, fmt.Sprintf("header %s: C# %q; Go %q", name, a.Header.Values(name), b.Header.Values(name)))
		}
	}
	left, err := decode(a.Body)
	if err != nil {
		differences = append(differences, "C# body: "+err.Error())
	}
	right, err2 := decode(b.Body)
	if err2 != nil {
		differences = append(differences, "Go body: "+err2.Error())
	}
	if err == nil && err2 == nil {
		differences = append(differences, diffJSON("$", left, right)...)
	}
	return differences
}

func diffJSON(path string, a, b any) []string {
	if reflect.DeepEqual(a, b) {
		return nil
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		ordered := make([]string, 0, len(keys))
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		var result []string
		for _, k := range ordered {
			av, ap := am[k]
			bv, bp := bm[k]
			if !ap || !bp {
				result = append(result, fmt.Sprintf("%s.%s: presence C# %t; Go %t", path, k, ap, bp))
				continue
			}
			result = append(result, diffJSON(path+"."+k, av, bv)...)
		}
		return result
	}
	aa, aok := a.([]any)
	ba, bok := b.([]any)
	if aok && bok && len(aa) == len(ba) {
		var result []string
		for i := range aa {
			result = append(result, diffJSON(fmt.Sprintf("%s[%d]", path, i), aa[i], ba[i])...)
		}
		return result
	}
	return []string{fmt.Sprintf("%s: C# %#v; Go %#v", path, a, b)}
}

// decode preserves integer width, null/presence, and array order. Only object
// ordering and insignificant JSON whitespace are normalized; no deviations waived.
func decode(body []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	v, err := value(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON: %v", err)
	}
	return v, nil
}

func value(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t == json.Delim('{') {
		m := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := m[name]; exists {
				return nil, fmt.Errorf("duplicate object member %q", name)
			}
			v, err := value(d)
			if err != nil {
				return nil, err
			}
			m[name] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("invalid object terminator: %v", err)
		}
		return m, nil
	}
	if t == json.Delim('[') {
		a := []any{}
		for d.More() {
			v, err := value(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("invalid array terminator: %v", err)
		}
		return a, nil
	}
	if _, ok := t.(json.Delim); ok {
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	}
	return t, nil
}

func checkEnvelope(e exchange) error {
	if e.Header.Get("X-Correlation-ID") != correlationID {
		return fmt.Errorf("correlation header not echoed")
	}
	v, err := decode(e.Body)
	if err != nil {
		return err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("response is not an envelope")
	}
	if m["correlationId"] != correlationID {
		return fmt.Errorf("correlation envelope not echoed")
	}
	for _, name := range []string{"paging", "isSuccess", "isAuthorized", "isValid", "isReady", "hasExceptions", "exceptionMessages", "exceptionStackTrace", "validationResults"} {
		if _, ok := m[name]; !ok {
			return fmt.Errorf("missing envelope member %s", name)
		}
	}
	return nil
}
