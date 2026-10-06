// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
)

type finishCommand struct{}

type rollbackOwner struct {
	commits, rollbacks int
	rollbackError      error
}

func (o *rollbackOwner) Commit(context.Context) (CommitResult, error) {
	o.commits++
	return CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}, nil
}
func (o *rollbackOwner) Rollback() error { o.rollbacks++; return o.rollbackError }

func TestFinishRollsBackWhenFrameIsUnavailableAndRetainsFailure(t *testing.T) {
	rollbackFailure := errors.New("rollback failed")
	owner := &rollbackOwner{rollbackError: rollbackFailure}
	tx := &transaction{owner: owner}
	i := &Integration{frame: commands.NewStateKey[*commandFrame]()}
	var registry commands.Registry
	err := commands.Register[finishCommand](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ finishCommand) (commands.NoResponse, error) {
		first, firstErr := i.finish(ctx, inv, tx, true)
		if first.Disposition != commands.NotCommitted || !errors.Is(firstErr, commands.ErrNoContext) || !errors.Is(firstErr, rollbackFailure) {
			t.Fatal(first, firstErr)
		}
		second, secondErr := i.finish(ctx, inv, tx, true)
		if second != first || !errors.Is(secondErr, commands.ErrNoContext) || !errors.Is(secondErr, rollbackFailure) || owner.commits != 0 || owner.rollbacks != 1 {
			t.Fatal(second, secondErr, owner)
		}
		tx.mu.Lock()
		stored, storedErr := tx.result.Report, tx.failure
		tx.mu.Unlock()
		if stored != first || !errors.Is(storedErr, commands.ErrNoContext) || !errors.Is(storedErr, rollbackFailure) {
			t.Fatal(stored, storedErr)
		}
		return commands.NoResponse{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.Execute(t.Context(), finishCommand{})
	if err != nil || !result.IsSuccess() || owner.rollbacks != 1 {
		t.Fatal(result, err, owner)
	}
}
