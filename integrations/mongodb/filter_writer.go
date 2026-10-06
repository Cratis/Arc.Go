// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The pinned driver buffers whole documents inside NewDocumentWriter. An
// io.Writer limit is therefore too late. Intercept every ValueWriter operation
// before the driver's buffer grows. Do not embed ValueWriter: that could expose
// private raw-copy fast paths which bypass this accounting.
type filterBudget struct {
	remaining int
	exceeded  bool
}

type filterWriter struct {
	writer bson.ValueWriter
	budget *filterBudget
}

type filterDocumentWriter struct {
	writer bson.DocumentWriter
	budget *filterBudget
}

type filterArrayWriter struct {
	writer bson.ArrayWriter
	budget *filterBudget
	index  int
}

func (b *filterBudget) take(n int) error {
	if n < 0 || n > b.remaining {
		b.exceeded = true
		return ErrLimit
	}
	b.remaining -= n
	return nil
}

func (w *filterWriter) scalar(n int, write func() error) error {
	if err := w.budget.take(n); err != nil {
		return err
	}
	return write()
}

func (w *filterWriter) WriteDocument() (bson.DocumentWriter, error) {
	if err := w.budget.take(5); err != nil {
		return nil, err
	}
	d, err := w.writer.WriteDocument()
	return &filterDocumentWriter{d, w.budget}, err
}

func (w *filterWriter) WriteArray() (bson.ArrayWriter, error) {
	if err := w.budget.take(5); err != nil {
		return nil, err
	}
	a, err := w.writer.WriteArray()
	return &filterArrayWriter{a, w.budget, 0}, err
}

func (w *filterWriter) WriteCodeWithScope(code string) (bson.DocumentWriter, error) {
	if err := w.budget.take(14 + len(code)); err != nil {
		return nil, err
	}
	d, err := w.writer.WriteCodeWithScope(code)
	return &filterDocumentWriter{d, w.budget}, err
}

func (w *filterDocumentWriter) WriteDocumentElement(key string) (bson.ValueWriter, error) {
	if err := w.budget.take(2 + len(key)); err != nil {
		return nil, err
	}
	v, err := w.writer.WriteDocumentElement(key)
	return &filterWriter{v, w.budget}, err
}

func (w *filterDocumentWriter) WriteDocumentEnd() error { return w.writer.WriteDocumentEnd() }

func (w *filterArrayWriter) WriteArrayElement() (bson.ValueWriter, error) {
	if err := w.budget.take(2 + len(strconv.Itoa(w.index))); err != nil {
		return nil, err
	}
	w.index++
	v, err := w.writer.WriteArrayElement()
	return &filterWriter{v, w.budget}, err
}

func (w *filterArrayWriter) WriteArrayEnd() error { return w.writer.WriteArrayEnd() }

func (w *filterWriter) WriteBinary(value []byte) error {
	return w.scalar(5+len(value), func() error { return w.writer.WriteBinary(value) })
}
func (w *filterWriter) WriteBinaryWithSubtype(value []byte, subtype byte) error {
	size := 5 + len(value)
	if subtype == 2 {
		size += 4
	}
	return w.scalar(size, func() error { return w.writer.WriteBinaryWithSubtype(value, subtype) })
}
func (w *filterWriter) WriteBoolean(value bool) error {
	return w.scalar(1, func() error { return w.writer.WriteBoolean(value) })
}
func (w *filterWriter) WriteDBPointer(ns string, id bson.ObjectID) error {
	return w.scalar(17+len(ns), func() error { return w.writer.WriteDBPointer(ns, id) })
}
func (w *filterWriter) WriteDateTime(value int64) error {
	return w.scalar(8, func() error { return w.writer.WriteDateTime(value) })
}
func (w *filterWriter) WriteDecimal128(value bson.Decimal128) error {
	return w.scalar(16, func() error { return w.writer.WriteDecimal128(value) })
}
func (w *filterWriter) WriteDouble(value float64) error {
	return w.scalar(8, func() error { return w.writer.WriteDouble(value) })
}
func (w *filterWriter) WriteInt32(value int32) error {
	return w.scalar(4, func() error { return w.writer.WriteInt32(value) })
}
func (w *filterWriter) WriteInt64(value int64) error {
	return w.scalar(8, func() error { return w.writer.WriteInt64(value) })
}
func (w *filterWriter) WriteJavascript(value string) error {
	return w.scalar(5+len(value), func() error { return w.writer.WriteJavascript(value) })
}
func (w *filterWriter) WriteMaxKey() error { return w.scalar(0, w.writer.WriteMaxKey) }
func (w *filterWriter) WriteMinKey() error { return w.scalar(0, w.writer.WriteMinKey) }
func (w *filterWriter) WriteNull() error   { return w.scalar(0, w.writer.WriteNull) }
func (w *filterWriter) WriteObjectID(value bson.ObjectID) error {
	return w.scalar(12, func() error { return w.writer.WriteObjectID(value) })
}
func (w *filterWriter) WriteRegex(pattern, options string) error {
	return w.scalar(2+len(pattern)+len(options), func() error { return w.writer.WriteRegex(pattern, options) })
}
func (w *filterWriter) WriteString(value string) error {
	return w.scalar(5+len(value), func() error { return w.writer.WriteString(value) })
}
func (w *filterWriter) WriteSymbol(value string) error {
	return w.scalar(5+len(value), func() error { return w.writer.WriteSymbol(value) })
}
func (w *filterWriter) WriteTimestamp(t, i uint32) error {
	return w.scalar(8, func() error { return w.writer.WriteTimestamp(t, i) })
}
func (w *filterWriter) WriteUndefined() error { return w.scalar(0, w.writer.WriteUndefined) }
