// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"math"
	"reflect"

	"github.com/cratis/arc.go/validation"
)

// PageNumber is a zero-based int32 request page.
type PageNumber int32

// PageSize is an int32 request window size.
type PageSize int32

// Paging describes active paging. Zero is not paged.
type Paging struct {
	Page    PageNumber
	Size    PageSize
	IsPaged bool
}

// Skip computes using int64 arithmetic, clamped to [0, MaxInt32].
func (p Paging) Skip() int32 {
	skip := int64(p.Page) * int64(p.Size)
	if skip < 0 {
		return 0
	}
	if skip > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(skip)
}

// Validate checks only active requests, preserving C# concept messages/member names.
func (p Paging) Validate() []validation.Result {
	if !p.IsPaged {
		return nil
	}
	var findings []validation.Result
	if p.Page < 0 {
		findings = append(findings, validation.Result{Severity: validation.Error, Message: "Page number must be greater than or equal to 0", Members: []string{"Page"}})
	}
	if p.Size <= 0 {
		findings = append(findings, validation.Result{Severity: validation.Error, Message: "Page size must be greater than 0", Members: []string{"Size"}})
	}
	return findings
}

// Parameters supplies provider-neutral request paging and sorting.
type Parameters struct {
	Paging  Paging
	Sorting Sorting
}

// Page declares data already windowed by the performer; it is never paged twice.
// Items are borrowed; TotalItems counts the full authorized selection before paging.
type Page[T any] struct {
	Items      []T
	TotalItems int64
}
type pageValue interface {
	pageDataType() reflect.Type
	pageData() (any, int64)
}

func (p Page[T]) pageDataType() reflect.Type { return reflect.TypeFor[[]T]() }
func (p Page[T]) pageData() (any, int64)     { return p.Items, p.TotalItems }
