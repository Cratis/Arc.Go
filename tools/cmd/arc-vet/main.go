// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command arc-vet checks opted-in Arc artifacts using the Go analysis driver.
package main

import (
	"github.com/cratis/arc.go/tools/internal/artifacts"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	multichecker.Main(artifacts.DeclarationAnalyzer, artifacts.AuthoringAnalyzer)
}
