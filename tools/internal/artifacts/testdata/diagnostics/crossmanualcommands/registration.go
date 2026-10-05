// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package crossmanualcommands

import (
	"github.com/cratis/arc.go/commands"

	"github.com/cratis/arc.go/tools/internal/artifacts/testdata/diagnostics/crossmanualcommands/feature"
)

func Register(registrar commands.Registrar) error {
	return commands.Register[feature.Greet](registrar, commands.Handle(feature.Greet.Handle))
}

func RegisterIgnored(registrar commands.Registrar) error {
	return commands.Register[feature.IgnoredGreet](registrar, commands.Handle(feature.IgnoredGreet.Handle))
}
