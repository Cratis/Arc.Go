// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"

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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope findEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return ErrValue
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrValue
	}
	filter, err := decodeFilter(envelope.BSON)
	if err != nil {
		return err
	}
	q.Filter = filter
	return nil
}

func freezeFilter(registry *bson.Registry, filter bson.D) (bson.Raw, error) {
	var buffer bytes.Buffer
	encoder := bson.NewEncoder(bson.NewDocumentWriter(&buffer))
	encoder.SetRegistry(registry)
	if err := encoder.Encode(nonnilFilter(filter)); err != nil {
		return nil, &operationError{"freeze filter", err}
	}
	raw := bson.Raw(bytes.Clone(buffer.Bytes()))
	if err := raw.Validate(); err != nil {
		return nil, ErrValue
	}
	return raw, nil
}

func decodeFilter(raw bson.Raw) (bson.D, error) {
	if raw.Validate() != nil {
		return nil, ErrValue
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
