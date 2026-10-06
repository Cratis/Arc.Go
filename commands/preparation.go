// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

// Preparation distinguishes usable typed payloads from explicit early stop.
// Zero is invalid; nil payloads supplied by Provided are still usable typed values.
type Preparation[P any] struct {
	value       P
	control     Result[NoResponse]
	valid, stop bool
}

// Provided supplies a payload with successful preparation controls.
func Provided[P any](value P) Preparation[P] {
	return Preparation[P]{value: value, valid: true, control: Success([16]byte{})}
}

// ProvidedWith supplies a payload and controls subject to input-stage severity.
func ProvidedWith[P any](value P, control Result[NoResponse]) Preparation[P] {
	return Preparation[P]{value: value, valid: true, control: control}
}

// StopProviding explicitly skips Handle even when control indicates success.
func StopProviding[P any](control Result[NoResponse]) Preparation[P] {
	return Preparation[P]{valid: true, stop: true, control: control}
}
