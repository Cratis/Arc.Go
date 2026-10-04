// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package capability_test

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// These are unchanged upstream JSON Schema Test Suite resources, not locally
// invented URI fixtures. The provenance and MIT license are recorded alongside.
//
//go:embed uri-tests.json
var officialURITests []byte

//go:embed uri-reference-tests.json
var officialURIReferenceTests []byte

//go:embed uri-tests-LICENSE
var officialURITestsLicense []byte

// RFC 3986 Appendix A productions, with IPv6address delegated to netip below.
// This is URI syntax, not URL normalization, scheme-specific validity or IRI
// syntax. In particular, do not unescape, trim or resolve the supplied string.
const (
	officialURIUnreserved = `[A-Za-z0-9._~-]`
	officialURISubDelims  = `[!$&'()*+,;=]`
	officialURIPercent    = `%[0-9A-Fa-f]{2}`
	officialURIRegChar    = `(?:` + officialURIUnreserved + `|` + officialURISubDelims + `|` + officialURIPercent + `)`
	officialURIPChar      = `(?:` + officialURIRegChar + `|[:@])`
	officialURISegmentNC  = `(?:` + officialURIRegChar + `|@)+`
	officialURIPathTail   = `(?:/` + officialURIPChar + `*)*`
	officialURIPathAbs    = `/(?:` + officialURIPChar + `+` + officialURIPathTail + `)?`
	officialURIIPvFuture  = `[vV][0-9A-Fa-f]+\.(?:` + officialURIUnreserved + `|` + officialURISubDelims + `|:)+`
	// IPv4-looking reg-names remain valid, even when not IPv4 addresses. Only
	// bracketed IPv6 literals require strict address validation (RFC 3986 3.2.2).
	officialURIHost      = `(?:\[(?P<ip>[0-9A-Fa-f:.]+|` + officialURIIPvFuture + `)\]|` + officialURIRegChar + `*)`
	officialURIAuthority = `(?:(?:` + officialURIRegChar + `|:)*@)?` + officialURIHost + `(?::[0-9]*)?`
	officialURIHierarchy = `//` + officialURIAuthority + officialURIPathTail + `|` + officialURIPathAbs + `|`
	officialURISuffix    = `(?:\?(?:` + officialURIPChar + `|[/?])*)?(?:#(?:` + officialURIPChar + `|[/?])*)?`
)

var officialURIGrammar = regexp.MustCompile(`\A[A-Za-z][A-Za-z0-9+.-]*:(?:` + officialURIHierarchy + officialURIPChar + `+` + officialURIPathTail + `|)` + officialURISuffix + `\z`)
var officialRelativeURIGrammar = regexp.MustCompile(`\A(?:` + officialURIHierarchy + officialURISegmentNC + officialURIPathTail + `|)` + officialURISuffix + `\z`)

func officialMatchesURI(grammar *regexp.Regexp, text string) bool {
	parts := grammar.FindStringSubmatch(text)
	if parts == nil {
		return false
	}
	literal := parts[grammar.SubexpIndex("ip")]
	if literal == "" || strings.HasPrefix(literal, "v") || strings.HasPrefix(literal, "V") {
		return true // Non-literal host or an IPvFuture matched by the grammar.
	}
	address, err := netip.ParseAddr(literal)
	return err == nil && address.Is6() && address.Zone() == ""
}

func officialURIFormat(reference bool) func(any) error {
	return func(value any) error {
		text, ok := value.(string)
		if !ok {
			return nil // JSON Schema string formats do not constrain other types.
		}
		if officialMatchesURI(officialURIGrammar, text) || (reference && officialMatchesURI(officialRelativeURIGrammar, text)) {
			return nil
		}
		return fmt.Errorf("not RFC 3986 URI syntax (relative reference allowed: %t)", reference)
	}
}

func officialURIGroup(t *testing.T, data []byte, format string, count int) map[string]any {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	groups, ok := value.([]any)
	if !ok || len(groups) != 1 {
		t.Fatal("URI test corpus must contain its one complete upstream group")
	}
	group := officialObject(groups[0])
	schema := officialObject(group["schema"])
	tests, ok := group["tests"].([]any)
	if schema["$schema"] != exactDialect || schema["format"] != format || !ok || len(tests) != count {
		t.Fatalf("URI corpus schema/count changed: %v, %d", schema, len(tests))
	}
	return group
}

func TestOfficialCapabilityURIFormats(t *testing.T) {
	for _, fixture := range []struct {
		format string
		data   []byte
		count  int
	}{
		{"uri", officialURITests, 47},
		{"uri-reference", officialURIReferenceTests, 31},
	} {
		t.Run(fixture.format, func(t *testing.T) {
			group := officialURIGroup(t, fixture.data, fixture.format, fixture.count)
			for _, assertFormats := range []bool{true, false} {
				mode := "assertion"
				if !assertFormats {
					mode = "annotation"
				}
				t.Run(mode, func(t *testing.T) {
					calls := 0
					compiler := officialCompiler(&calls, assertFormats)
					if err := compiler.AddResource(exactResource, group["schema"]); err != nil {
						t.Fatal(err)
					}
					schema, err := compiler.Compile(exactResource)
					if err != nil {
						t.Fatal(err)
					}
					for _, value := range group["tests"].([]any) {
						tc := officialObject(value)
						t.Run(tc["description"].(string), func(t *testing.T) {
							valid := tc["valid"].(bool) || !assertFormats
							if err := schema.Validate(tc["data"]); (err == nil) != valid {
								t.Fatalf("upstream URI case %v: valid=%t want=%t: %v", tc["data"], err == nil, valid, err)
							}
						})
					}
					if calls != 0 {
						t.Fatalf("URI formats attempted resource I/O: %d", calls)
					}
				})
			}
		})
	}
}

func TestOfficialCapabilityURIReferenceOperationStructure(t *testing.T) {
	group := officialURIGroup(t, officialURIReferenceTests, "uri-reference", 31)
	structure, query, calls := officialHarness(t)
	for _, isQuery := range []bool{false, true} {
		label := "native"
		if isQuery {
			label = "QUERY"
		}
		t.Run(label, func(t *testing.T) {
			for _, value := range group["tests"].([]any) {
				tc := officialObject(value)
				t.Run(tc["description"].(string), func(t *testing.T) {
					data := officialData(t, func(document map[string]any) {
						officialOperationObject(document, isQuery)["externalDocs"] = map[string]any{"url": tc["data"]}
					})
					// The official URL property also requires type:string, unlike
					// the upstream format-only schema. Preserve both assertions.
					_, isString := tc["data"].(string)
					officialAssertCase(t, data, tc["valid"].(bool) && isString, structure, query, calls)
				})
			}
		})
	}
}
