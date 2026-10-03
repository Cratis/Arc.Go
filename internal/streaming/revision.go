// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package streaming implements transport-independent bounded connection state.
// It starts no workers; transports own opening, delivery and cleanup joins.
package streaming

import (
	"bytes"
	"errors"
	"strconv"
)

// MaxRevision is JavaScript's largest exactly representable integer.
const MaxRevision = 9007199254740991

// ErrRevision rejects out-of-range or non-integer-lexical protocol revisions.
var ErrRevision = errors.New("invalid subscription revision")

// Revision is subscription ordering, never a collection snapshot version.
// Wire DTOs use *Revision with omitempty: nil means legacy, including JSON null.
type Revision uint64

// Valid checks the protocol's inclusive positive safe-integer range.
func (r Revision) Valid() bool { return r > 0 && r <= MaxRevision }

// UnmarshalJSON rejects strings, exponents, fractions, signs and rounded values.
// A nullable DTO pointer handles explicit null without invoking this method.
func (r *Revision) UnmarshalJSON(data []byte) error {
	if r == nil {
		return ErrRevision
	}
	node := bytes.TrimSpace(data)
	if len(node) == 0 || node[0] == '0' {
		return ErrRevision
	}
	for _, c := range node {
		if c < '0' || c > '9' {
			return ErrRevision
		}
	}
	value, err := strconv.ParseUint(string(node), 10, 64)
	if err != nil || !Revision(value).Valid() {
		return ErrRevision
	}
	*r = Revision(value)
	return nil
}

// MarshalJSON refuses invalid locally constructed revisions.
func (r Revision) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return nil, ErrRevision
	}
	return []byte(strconv.FormatUint(uint64(r), 10)), nil
}

func copyRevision(r *Revision) *Revision {
	if r == nil {
		return nil
	}
	value := *r
	return &value
}
