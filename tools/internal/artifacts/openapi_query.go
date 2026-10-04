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
// expressed in JSON Schema; neither can int32's integer-token spelling (1.0 and
// 1e0 are mathematical integers but malformed for the reader). Describe these
// parser boundaries rather than claiming schema validation proves acceptance.
// Sorting direction is a pipeline validation rule,
// not a JSON type: an unrecognized direction with a nonempty field returns 400.
func openAPIQueryRequest() openAPIObject {
	integer := func() openAPIObject {
		return openAPIObject{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}
	}
	paging := openAPIQueryObject(openAPIObject{"page": integer(), "pageSize": integer()})
	paging["description"] = "Only positive pageSize enables paging. Nonpageable snapshot results ignore valid paging. Missing integers default to zero; explicit null integers are malformed. JSON Schema checks mathematical integers, not token spelling: the reader rejects decimal/exponent tokens such as 1.0 and 1e0."
	sorting := openAPIQueryObject(openAPIObject{
		"field":     openAPINull(openAPIObject{"type": "string"}, true),
		"direction": openAPIObject{},
	})
	// With no matching field, or an empty/null field, this inner schema is
	// satisfied and its negation is false. A nonempty field (including any
	// reader-supported case alias) activates direction's nullable string shape.
	// Field's own type remains constrained even when direction is ignored.
	sorting["if"] = openAPIObject{"not": openAPIQueryObject(openAPIObject{
		"field": openAPIObject{"enum": []any{"", nil}},
	})}
	sorting["then"] = openAPIQueryObject(openAPIObject{
		"direction": openAPINull(openAPIObject{"type": "string"}, true),
	})
	sorting["description"] = "A nonempty field enables sorting; otherwise direction is ignored regardless of its JSON type. With a nonempty field, direction must be a string or null; omitted/null direction means ascending. asc, ascending, desc and descending are accepted case-insensitively; other strings then produce a query validation error. Nonpageable results ignore valid sorting."
	request := openAPIQueryObject(openAPIObject{
		"arguments": openAPINull(openAPIObject{"type": "object"}, true),
		"paging":    openAPINull(paging, true),
		"sorting":   openAPINull(sorting, true),
	})
	request["description"] = "Exactly one JSON object. All members are optional; unknown members are ignored. Known member and argument names are case-insensitive; duplicate known members and duplicate argument names are malformed. Null arguments/paging/sorting act as omitted. This operation declares no arguments; supplied arguments do not become handler parameters."
	return request
}

// Keep canonical spelling visible to ordinary schema consumers while applying
// the same shape to case variants accepted by the built-in strings.ToLower
// reader. Go also maps U+0130 to i (and U+212A to k); these are not unknown
// members. Use ECMA-compatible character classes, not Go-only (?i).
func openAPIQueryObject(properties openAPIObject) openAPIObject {
	patterns := openAPIObject{}
	for name, schema := range properties {
		var pattern strings.Builder
		pattern.WriteByte('^')
		for _, c := range name {
			lower := strings.ToLower(string(c))
			upper := strings.ToUpper(string(c))
			extra := ""
			switch lower {
			case "i":
				extra = "İ"
			case "k":
				extra = "K"
			}
			pattern.WriteString("[" + lower + upper + extra + "]")
		}
		pattern.WriteByte('$')
		patterns[pattern.String()] = schema
	}
	return openAPIObject{"type": "object", "properties": properties, "patternProperties": patterns}
}
