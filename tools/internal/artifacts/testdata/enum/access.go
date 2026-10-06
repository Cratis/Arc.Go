// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

//arc:namespace EnumContract
package access

// Access exercises an imported, enum-only package and the C# Flags corpus.
//arc:enum parse=int32 flags=true
type Access int32

const (
	None  Access = 0
	Read  Access = 1
	Write Access = 4
	Alias Access = 4
	High  Access = 1 << 30
	Sign  Access = -1 << 31
)
