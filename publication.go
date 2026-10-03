// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"

	"github.com/cratis/arc.go/commands"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func encode(ctx context.Context, value any) (body []byte, err error) {
	err = boundary.Call(ctx, func(context.Context) error { var err error; body, err = serialization.Marshal(value); return err })
	return
}
func safeFindings(findings []validation.Result) []validation.Result {
	for i := range findings {
		findings[i].State = nil
	}
	return findings
}
func failedCommand(d commands.Details) (any, int) {
	d.ValidationResults = safeFindings(d.ValidationResults)
	d.ExceptionMessages = []string{boundary.InternalErrorMessage}
	d.ExceptionStackTrace = ""
	result := commands.NewResult(d, serialization.Optional[any]{})
	return result, result.StatusCode()
}
func publicationFailure(value any) (any, int) {
	switch r := value.(type) {
	case commands.Result[any]:
		return failedCommand(r.Details())
	case commands.Result[commands.NoResponse]:
		return failedCommand(r.Details())
	case queries.Result[any]:
		d := r.Details()
		d.ChangeSet = nil
		d.ValidationResults = safeFindings(d.ValidationResults)
		d.ExceptionMessages = []string{boundary.InternalErrorMessage}
		d.ExceptionStackTrace = ""
		result := queries.NewResult(d, serialization.Optional[any]{})
		return result, result.StatusCode()
	default:
		return nil, 500
	}
}
