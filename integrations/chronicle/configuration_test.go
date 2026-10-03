// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

func TestConfiguredCommandsRequireExactRegistrationAtBuild(t *testing.T) {
	for _, which := range []string{"unregistered", "value configured pointer registered", "pointer configured value registered"} {
		t.Run(which, func(t *testing.T) {
			factory := &fakeFactory{}
			builder, integration := setup(t, factory)
			if which == "pointer configured value registered" {
				must(t, c.ConfigureCommand[*Change](integration, c.CommandOptions{}))
			} else {
				must(t, c.ConfigureCommand[Change](integration, c.CommandOptions{}))
			}
			must(t, integration.Install(builder))
			switch which {
			case "value configured pointer registered":
				must(t, commands.Register[*Change](builder, commands.Void(func(*Change, context.Context) error { return nil })))
			case "pointer configured value registered":
				must(t, commands.Register[Change](builder, commands.Void(func(Change, context.Context) error { return nil })))
			}
			app, err := builder.Build()
			if app != nil || !errors.Is(err, c.ErrInvalid) || !strings.Contains(err.Error(), "ConfigureCommand[") || !strings.Contains(err.Error(), "exact registered command type") {
				t.Fatalf("Build = %v, %v", app, err)
			}
			if factory.begins != 0 {
				t.Fatal("Build activated Chronicle")
			}
		})
	}
}

func TestConfiguredCommandCanRegisterAfterInstall(t *testing.T) {
	factory := &fakeFactory{}
	builder, integration := setup(t, factory)
	must(t, c.ConfigureCommand[Change](integration, c.CommandOptions{}))
	must(t, integration.Install(builder))
	must(t, commands.Register[Change](builder, commands.Void(func(Change, context.Context) error { return nil })))
	_, err := builder.Build()
	must(t, err)
	if factory.begins != 0 {
		t.Fatal("Build activated Chronicle")
	}
}

func TestConfiguredPointerCommandCanRegisterAfterInstall(t *testing.T) {
	builder, integration := setup(t, &fakeFactory{})
	must(t, c.ConfigureCommand[*Change](integration, c.CommandOptions{}))
	must(t, integration.Install(builder))
	must(t, commands.Register[*Change](builder, commands.Void(func(*Change, context.Context) error { return nil })))
	_, err := builder.Build()
	must(t, err)
}
