// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import fconcepts "github.com/cratis/fundamentals.go/concepts"

// Concept declares a domain value's scalar representation. The value must also
// implement JSON and text marshalers, and its pointer JSON and text unmarshalers.
// Arc validates declarations without invoking ConceptValue or the codecs.
// T must be an exact supported primitive or shared scalar, not another concept.
// This alias has the same identity as Fundamentals.Go's Concept.
type Concept[T any] = fconcepts.Concept[T]
