// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"strings"
	"unicode/utf8"

	"github.com/cratis/arc.go/tenancy"
)

// DatabaseName preserves case and Unicode. NotSet and Default use base;
// other tenants use base+"+"+tenant. This selects coordinates, not authorization.
// Database names must be 1–63 UTF-8 bytes and satisfy MongoDB's cross-platform
// restrictions. No sanitization, truncation, or case folding is performed.
func DatabaseName(base string, tenant tenancy.ID) (string, error) {
	if !validDatabase(base) {
		return "", ErrConfiguration
	}
	name := base
	if !tenant.IsDefault() {
		name += "+" + tenant.String()
	}
	if !validDatabase(name) {
		return "", ErrConfiguration
	}
	return name, nil
}

func validDatabase(name string) bool {
	return name != "" && len(name) < 64 && utf8.ValidString(name) &&
		!strings.ContainsAny(name, "/\\. \"$*<>:|?\x00")
}

func validCollection(database, name string) bool {
	// Unsharded namespace limit. Sharded deployments impose the additional
	// server-side 235-byte limit; see the storage profile.
	return name != "" && utf8.ValidString(name) &&
		!strings.HasPrefix(name, "system.") && !strings.ContainsAny(name, "$\x00") &&
		len(database)+1+len(name) <= 255
}

func validField(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.ContainsAny(name, ".$\x00")
}
