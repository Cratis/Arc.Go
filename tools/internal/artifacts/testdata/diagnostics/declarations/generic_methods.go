// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

//go:build go1.27

package declarations

func (Model) GenericMethod[T any]() (Model, error) { return Model{}, nil } // want ARC0014 GenericMethod

// Unrelated generic helpers remain outside query discovery.
func (Model) UnrelatedGeneric[T any]() (Wrong, error) { return Wrong{}, nil }
