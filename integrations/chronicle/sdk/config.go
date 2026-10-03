// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package sdk adapts Chronicle.Go to Arc's optional integration. A supplied client
// is borrowed; this package never closes it implicitly or connects during Build.
package sdk

import (
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/identity"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/chronicle.go"
	"log/slog"
)

// Config selects one store; request tenancy selects its namespace. The client is
// borrowed by default and must outlive Arc commands, model reads, aggregates and reactors.
type Config struct {
	Store chronicle.StoreName
	// OwnClient transfers closure responsibility to Integration.Close, never an implicit Arc hook.
	OwnClient bool
	// StartupNamespaces is copied; Integration.Start connects/registers these after Arc starts.
	StartupNamespaces []chronicle.Namespace
	// Namespace maps already-authorized tenancy; nil maps unset/default to Default.
	Namespace func(tenancy.ID) (chronicle.Namespace, error)
	Actor     func(identity.Principal) integration.Actor
	Audit     func(commands.CommandContext) map[string]string
	Logger    *slog.Logger
}
