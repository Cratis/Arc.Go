// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import "strings"

// openAPIQueryRequest describes the built-in reader, not a fabricated request
// model or an application argument binder. This checkpoint admits no declared
// arguments and no pageable result. The reader nevertheless parses these members
// before the pipeline ignores paging/sorting for a nonpageable snapshot.
//
// Source: Arc 7c1e780, Arc.Core/Queries/QueryRequestEnvelope.cs and
// BodyQueryRequestReader.cs; Go queries/request_query.go. The Go reader requires
// an object (unlike the C# null-envelope fallback), rejects duplicate known names
// case-insensitively, and rejects trailing JSON. Duplicate JSON members cannot be
// expressed in JSON Schema; describe that parser boundary rather than claiming
// schema validation proves it. Sorting direction is a pipeline validation rule,
// not a JSON type: an unrecognized direction with a nonempty field returns 400.
func openAPIQueryRequest() openAPIObject {
	integer := func() openAPIObject {
		return openAPIObject{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}
	}
	paging := openAPIQueryObject(openAPIObject{"page": integer(), "pageSize": integer()})
	paging["description"] = "Only positive pageSize enables paging. Nonpageable snapshot results ignore valid paging. Missing integers default to zero; explicit null integers are malformed."
	sorting := openAPIQueryObject(openAPIObject{
		"field":     openAPINull(openAPIObject{"type": "string"}, true),
		"direction": openAPINull(openAPIObject{"type": "string"}, true),
	})
	sorting["description"] = "A nonempty field enables sorting; omitted/null direction means ascending. asc, ascending, desc and descending are accepted case-insensitively; other directions then produce a query validation error. Nonpageable results ignore valid sorting."
	request := openAPIQueryObject(openAPIObject{
		"arguments": openAPINull(openAPIObject{"type": "object"}, true),
		"paging":    openAPINull(paging, true),
		"sorting":   openAPINull(sorting, true),
	})
	request["description"] = "Exactly one JSON object. All members are optional; unknown members are ignored. Known member and argument names are case-insensitive; duplicate known members and duplicate argument names are malformed. Null arguments/paging/sorting act as omitted. This operation declares no arguments; supplied arguments do not become handler parameters."
	return request
}

// Keep canonical spelling visible to ordinary schema consumers while applying
// the same shape to each ASCII case variant accepted by the built-in reader.
// This uses ordinary ECMA-compatible character classes, not Go-only (?i).
func openAPIQueryObject(properties openAPIObject) openAPIObject {
	patterns := openAPIObject{}
	for name, schema := range properties {
		var pattern strings.Builder
		pattern.WriteByte('^')
		for _, c := range name {
			lower := strings.ToLower(string(c))
			upper := strings.ToUpper(string(c))
			pattern.WriteString("[" + lower + upper + "]")
		}
		pattern.WriteByte('$')
		patterns[pattern.String()] = schema
	}
	return openAPIObject{"type": "object", "properties": properties, "patternProperties": patterns}
}
