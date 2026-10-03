// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

func TestFailuresWithoutStagedWorkHaveNoPersistenceDisposition(t *testing.T) {
	for _, poison := range []bool{false, true} {
		t.Run(map[bool]string{false: "handler failure", true: "failed event admission"}[poison], func(t *testing.T) {
			f := &fakeFactory{}
			builder, integration := setup(t, f)
			must(t, integration.Install(builder))
			cause := errors.New("handler failed before staging")
			must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
				if poison {
					_, _ = integration.Handle(ctx, inv, c.EventForSource("a", struct{ Unknown string }{}))
					return commands.NoResponse{}, nil
				}
				return commands.NoResponse{}, cause
			})))
			result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
			var completion *commands.CompletionError
			if result.IsSuccess() || err == nil || errors.As(err, &completion) || result.Completion().Disposition != commands.NoPersistedWork || f.begins != 0 || f.commits != 0 || f.rollbacks != 0 {
				t.Fatal(result, err, f)
			}
			if !poison && !errors.Is(err, cause) {
				t.Fatal(err)
			}
		})
	}
}
