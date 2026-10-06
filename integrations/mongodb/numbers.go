// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Numeric strings are invariant, not locale-dependent. Limit exponent work
// before big.Rat allocation; Decimal128's exponent range fits within this bound.
var numericText = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func readNumber(r bson.ValueReader) (*big.Rat, error) {
	switch r.Type() {
	case bson.TypeInt32:
		value, err := r.ReadInt32()
		if err != nil {
			return nil, ErrValue
		}
		return new(big.Rat).SetInt64(int64(value)), nil
	case bson.TypeInt64:
		value, err := r.ReadInt64()
		if err != nil {
			return nil, ErrValue
		}
		return new(big.Rat).SetInt64(value), nil
	case bson.TypeDouble:
		value, err := r.ReadDouble()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, ErrValue
		}
		return new(big.Rat).SetFloat64(value), nil
	case bson.TypeDecimal128:
		value, err := r.ReadDecimal128()
		if err != nil {
			return nil, ErrValue
		}
		return parseNumber(value.String())
	case bson.TypeString:
		value, err := r.ReadString()
		if err != nil {
			return nil, ErrValue
		}
		return parseNumber(value)
	}
	return nil, ErrValue
}

func parseNumber(text string) (*big.Rat, error) {
	if len(text) > 128 || !numericText.MatchString(text) {
		return nil, ErrValue
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, err := strconv.ParseInt(text[i+1:], 10, 32)
		if err != nil || exponent < -10_000 || exponent > 10_000 {
			return nil, ErrValue
		}
	}
	number, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, ErrValue
	}
	return number, nil
}

func decodeNumber(r bson.ValueReader, v reflect.Value) error {
	if (v.Kind() == reflect.Float32 || v.Kind() == reflect.Float64) && r.Type() == bson.TypeDouble {
		value, err := r.ReadDouble()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || v.OverflowFloat(value) {
			return ErrValue
		}
		v.SetFloat(value) // Preserve IEEE negative zero on Double reads.
		return nil
	}
	number, err := readNumber(r)
	if err != nil {
		return err
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !number.IsInt() || !number.Num().IsInt64() || v.OverflowInt(number.Num().Int64()) {
			return ErrValue
		}
		v.SetInt(number.Num().Int64())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !number.IsInt() || !number.Num().IsUint64() || v.OverflowUint(number.Num().Uint64()) {
			return ErrValue
		}
		v.SetUint(number.Num().Uint64())
	case reflect.Float32, reflect.Float64:
		value, _ := number.Float64()
		if math.IsInf(value, 0) || v.OverflowFloat(value) {
			return ErrValue
		}
		v.SetFloat(value)
	default:
		return ErrValue
	}
	return nil
}
