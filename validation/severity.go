// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import (
	"strconv"
	"strings"
)

// SeverityOptions combines a caller allowance with an inclusive command floor.
type SeverityOptions struct {
	// Allowed retains only severities strictly greater than this value.
	Allowed *Severity
	// BlockOn is inclusive and always blocks Unknown when present.
	BlockOn *Severity
}

// Policy is immutable and concurrent-safe. Zero retains only Error findings.
type Policy struct {
	threshold    Severity
	configured   bool
	blockUnknown bool
}

// NewPolicy copies configuration, rejecting severities outside 0–3. An explicit
// floor cannot be weakened by Allowed, even a trusted Allowed=Error.
func NewPolicy(options SeverityOptions) (Policy, error) {
	p := Policy{threshold: Warning}
	if options.Allowed != nil {
		if !validSeverity(*options.Allowed) {
			return Policy{}, ErrInvalidSeverity
		}
		p.threshold = *options.Allowed
		p.configured = true
	}
	if options.BlockOn != nil {
		if !validSeverity(*options.BlockOn) {
			return Policy{}, ErrInvalidSeverity
		}
		floor := *options.BlockOn - 1
		// A floor supplies its own threshold when no allowance was specified.
		if options.Allowed == nil || floor < p.threshold {
			p.threshold = floor
		}
		p.configured = true
		p.blockUnknown = true
	}
	return p, nil
}

// Blocks reports whether a finding stops execution; unsupported numbers block.
func (p Policy) Blocks(severity Severity) bool {
	if !validSeverity(severity) {
		return true
	}
	if p.blockUnknown && severity == Unknown {
		return true
	}
	if !p.configured {
		return severity == Error
	}
	return severity > p.threshold
}

// Filter returns independent copies of blocking findings, borrowing State.
// This does not make an authorization decision. Queries retain all findings.
func (p Policy) Filter(results []Result) []Result {
	filtered := make([]Result, 0, len(results))
	for _, result := range results {
		if p.Blocks(result.Severity) {
			filtered = append(filtered, result.Clone())
		}
	}
	return filtered
}

// AllowedSeverityHeader is the command ingress allowance header.
const AllowedSeverityHeader = "X-Allowed-Severity"

// AllowedFromHeader accepts exactly one trimmed signed decimal int32 in 0–3.
// Untrusted Error allowance is capped at Warning. Invalid input means default
// policy, not permission to ignore all findings.
func AllowedFromHeader(values []string) (Severity, bool) {
	if len(values) != 1 {
		return Unknown, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 32)
	if err != nil || n < 0 || n > 3 {
		return Unknown, false
	}
	severity := Severity(n)
	if severity == Error {
		severity = Warning
	}
	return severity, true
}
func validSeverity(s Severity) bool { return s >= Unknown && s <= Error }
