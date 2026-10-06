// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package unselectedcommands

import "context"

// Without artifact-selection directives, Handle alone is not evidence of
// forgotten generator opt-in. Registration may be owned by another package.
type Greet struct{ Name string }

func (c Greet) Handle(_ context.Context) (string, error) { return "Hello, " + c.Name, nil }
