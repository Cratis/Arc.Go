// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package importeddeclarations

import domain "github.com/cratis/arc.go/tools/internal/artifacts/testdata/diagnostics/declarationdomain"

type ModelAlias = domain.Model
type CommandAlias = domain.Command

type Handler struct{}

func (Handler) Handle(CommandAlias) error { return nil }

//arc:command
type Command struct{}

func (Command) Handle(model ModelAlias, optional *ModelAlias) error { return nil }
