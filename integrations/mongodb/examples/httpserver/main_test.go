// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"strings"
	"testing"
)

func TestLocalExampleRejectsMissingOrRemoteConfigurationWithoutIO(t *testing.T) {
	for _, config := range []struct{ uri, token, tenant string }{
		{},
		{uri: "mongodb://remote.example:27017", token: strings.Repeat("x", 32), tenant: "Demo"},
		{uri: "mongodb://127.0.0.1:27017", token: "short", tenant: "Demo"},
		{uri: "mongodb://127.0.0.1:27017", token: strings.Repeat("x", 32)},
	} {
		if err := run(t.Context(), config.uri, config.token, config.tenant); err == nil {
			t.Fatal("invalid local example configuration accepted")
		}
	}
}
