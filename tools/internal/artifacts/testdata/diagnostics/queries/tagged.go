//go:build arcdiagnostics

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

//arc:query model=View
func Tagged(args Arguments) (View, error) {
	_ = ConceptAlias(args.Name) // want ARC0015 args.Name
	panic("query executed")
}
