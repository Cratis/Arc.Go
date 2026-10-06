// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// MarshalJSON detaches a trusted provider instruction as a base64 BSON envelope,
// not a JSON projection of interface-valued BSON. This is an internal Arc value
// boundary, never an HTTP input format. Do not mutate Filter during encoding.
func (q Find[T]) MarshalJSON() ([]byte, error) {
	registry, err := NewRegistry(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	raw, err := freezeFilter(registry, q.Filter)
	if err != nil {
		return nil, err
	}
	return json.Marshal(findEnvelope{BSON: raw})
}

type findEnvelope struct {
	BSON []byte `json:"bson"`
}

// UnmarshalJSON validates and decodes a provider envelope failure-atomically.
// Native BSON integer widths, ordered nested documents and binary subtypes are
// retained. Applications must not bind this trusted selection from HTTP input.
func (q *Find[T]) UnmarshalJSON(data []byte) error {
	if q == nil {
		return ErrValue
	}
	// Bound the JSON representation before decoding allocates base64 output.
	// The small allowance admits formatting, not unbounded whitespace/escapes.
	if len(data) > base64.StdEncoding.EncodedLen(maxFilterBytes)+1024 {
		return ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope struct {
		BSON json.RawMessage `json:"bson"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return ErrValue
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrValue
	}
	var encoded string
	if err := json.Unmarshal(envelope.BSON, &encoded); err != nil {
		return ErrValue
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxFilterBytes) {
		return ErrLimit
	}
	// Only canonical padded envelopes are admitted. In particular, malformed
	// padding must not make the destination smaller than a decoded quartet.
	if len(encoded)%4 != 0 || strings.ContainsAny(encoded, "\r\n") {
		return ErrValue
	}
	size := base64.StdEncoding.DecodedLen(len(encoded))
	if len(encoded) > 0 && encoded[len(encoded)-1] == '=' {
		size--
	}
	if len(encoded) > 1 && encoded[len(encoded)-2] == '=' {
		size--
	}
	if size < 0 {
		return ErrValue
	}
	if size > maxFilterBytes {
		return ErrLimit
	}
	raw := make([]byte, size)
	count, err := base64.StdEncoding.Decode(raw, []byte(encoded))
	if err != nil {
		return ErrValue
	}
	filter, err := decodeFilter(raw[:count])
	if err != nil {
		return err
	}
	q.Filter = filter
	return nil
}

func freezeFilter(registry *bson.Registry, filter bson.D) (bson.Raw, error) {
	return freezeFilterBounded(registry, filter, maxFilterBytes)
}

func freezeFilterBounded(registry *bson.Registry, filter bson.D, limit int) (bson.Raw, error) {
	limit = min(limit, maxFilterBytes)
	if err := guardFilter(filter, limit); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	budget := &filterBudget{remaining: limit}
	writer := &filterWriter{bson.NewDocumentWriter(&buffer), budget}
	encoder := bson.NewEncoder(writer)
	encoder.SetRegistry(registry)
	if err := encoder.Encode(nonnilFilter(filter)); err != nil {
		if budget.exceeded {
			return nil, ErrLimit
		}
		return nil, &operationError{"freeze filter", err}
	}
	raw := bson.Raw(bytes.Clone(buffer.Bytes()))
	if err := guardRawDocument(raw, false, 0, limit); err != nil {
		return nil, ErrValue
	}
	return raw, nil
}

func decodeFilter(raw bson.Raw) (bson.D, error) {
	if err := guardRawDocument(raw, false, 0, maxFilterBytes); err != nil {
		return nil, err
	}
	// A BSON document's declared size must consume the entire envelope.
	if len(raw) < 5 || int64(len(raw)) != int64(int32(uint32(raw[0])|uint32(raw[1])<<8|uint32(raw[2])<<16|uint32(raw[3])<<24)) {
		return nil, ErrValue
	}
	var filter bson.D
	if err := bson.Unmarshal(raw, &filter); err != nil {
		return nil, ErrValue
	}
	return nonnilFilter(filter), nil
}
