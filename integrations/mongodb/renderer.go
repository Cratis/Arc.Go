// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Find is a trusted application-authored BSON selection, not request-supplied
// MongoDB syntax. Execute freezes Filter once; nil means an empty selection
// predicate. Do not mutate its graph concurrently with Execute. For Chronicle
// sinks, only approved cleartext fields may be filtered or sorted.
type Find[T any] struct {
	// Filter uses storage BSON names, including any declared overrides.
	Filter bson.D
}

// RendererOptions configures authorization, publication and retention bounds.
// Callbacks are synchronous, must honor cancellation and may run concurrently.
// The integration cannot stop a callback that ignores its context.
type RendererOptions[T any] struct {
	// RowFilter is mandatory and evaluated once after Arc admission. A nil
	// returned filter denies execution; explicit bson.D{} permits all rows.
	// The frozen predicate is ANDed with Filter, never overwritten by it.
	RowFilter func(context.Context, queries.QueryContext) (bson.D, error)
	// Release is mandatory for ChronicleOwned and rejected for ApplicationOwned.
	// It receives complete copied BSON, including ciphertext and lineage, before
	// typed decoding. The caller owns mapping and actual Chronicle release.
	// Return exactly the same row count/order/identities; failure publishes
	// neither data nor total. The callback must not mutate or retain its input.
	Release func(context.Context, []bson.Raw) ([]T, error)
	// MaxItems bounds retained documents; zero means 1000. Positive overrides
	// must be below MaxInt32, leaving space for an overflow-detection row.
	MaxItems int
	// MaxBSONBytes bounds retained raw BSON; zero means 16 MiB.
	MaxBSONBytes int64
	// Timeout bounds the whole operation; zero means ten seconds. Cursor Close
	// has a separate detached five-second budget. Negative options are invalid.
	Timeout time.Duration
}

// Renderer implements Arc's exact Find[T] -> []T snapshot boundary. Construct it
// with NewRenderer; zero is invalid. It borrows its immutable collection/client
// and is concurrently callable when application callbacks are. Count and find
// use primary/majority reads and simple collation, but are NOT one atomic
// snapshot. Driver read retry settings remain application-owned.
type Renderer[T any] struct {
	collection *Collection[T]
	options    RendererOptions[T]
	open       func(tenancy.ID) (snapshotCollection, error)
}

// NewRenderer validates/copies options without I/O, goroutines, callbacks or
// client ownership transfer. It does not retain an operation scope or context.
func NewRenderer[T any](collection *Collection[T], config RendererOptions[T]) (*Renderer[T], error) {
	if collection == nil || collection.client == nil || collection.registry == nil || config.RowFilter == nil ||
		config.MaxItems < 0 || config.MaxItems >= math.MaxInt32 || config.MaxBSONBytes < 0 || config.Timeout < 0 {
		return nil, ErrConfiguration
	}
	if collection.options.Ownership == ChronicleOwned && config.Release == nil {
		return nil, ErrReleaseRequired
	}
	if collection.options.Ownership == ApplicationOwned && config.Release != nil {
		return nil, ErrConfiguration
	}
	if config.MaxItems == 0 {
		config.MaxItems = 1000
	}
	if config.MaxBSONBytes == 0 {
		config.MaxBSONBytes = 16 << 20
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	return &Renderer[T]{collection: collection, options: config, open: func(tenant tenancy.ID) (snapshotCollection, error) {
		c, err := collection.forTenant(tenant)
		if err != nil {
			return nil, err
		}
		return driverCollection{c}, nil
	}}, nil
}

// Execute counts authorized rows, sorts/windows on the server, copies bounded
// cursor documents, then decodes or releases them. No partial page or total
// escapes on any failure, panic, cancellation or cursor cleanup error. Limits
// reject rather than silently truncate. Arc performs interception afterwards.
func (r *Renderer[T]) Execute(ctx context.Context, query Find[T], c queries.QueryContext) (result queries.RendererResult[[]T], err error) {
	if ctx == nil || r == nil || r.collection == nil || r.open == nil {
		return result, ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, r.options.Timeout)
	defer cancel()
	var cursor snapshotCursor
	defer func() {
		if value := recover(); value != nil {
			err = &execution.PanicError{Value: value, Stack: debug.Stack()}
		}
		if cursor != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = errors.Join(err, closeCursor(cleanup, cursor))
			stop()
		}
		err = errors.Join(err, ctx.Err())
		if err != nil {
			result = queries.RendererResult[[]T]{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	p := c.Parameters()
	if findings := p.Paging.Validate(); len(findings) != 0 {
		return result, &pagingFailure{findings}
	}
	if p.Paging.IsPaged && int64(p.Paging.Size) > int64(r.options.MaxItems) {
		return result, ErrLimit
	}
	sort, err := r.sort(p.Sorting)
	if err != nil {
		return result, err
	}
	row, err := r.options.RowFilter(ctx, c)
	if err != nil {
		return result, &operationError{"row authorization", err}
	}
	if row == nil {
		return result, ErrConfiguration
	}
	filter, err := r.freeze(bson.D{{Key: "$and", Value: bson.A{row, nonnilFilter(query.Filter)}}})
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	collection, err := r.open(c.Tenant())
	if err != nil {
		return result, err
	}
	total, err := collection.count(ctx, filter)
	if err != nil {
		return result, &operationError{"count", err}
	}
	if total < 0 {
		return result, ErrValue
	}
	// Arc's totalPages converts to int32. Check both the integer ceiling and
	// Arc's double-precision ceiling before that conversion.
	if p.Paging.IsPaged && total > 0 && ((total-1)/int64(p.Paging.Size)+1 > math.MaxInt32 || math.Ceil(float64(total)/float64(p.Paging.Size)) > math.MaxInt32) {
		return result, ErrLimit
	}
	if !p.Paging.IsPaged && total > int64(r.options.MaxItems) {
		return result, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	window := snapshotWindow{sort: sort, limit: int64(r.options.MaxItems) + 1}
	if p.Paging.IsPaged {
		window.skip, window.limit = int64(p.Paging.Skip()), int64(p.Paging.Size)
	}
	cursor, err = collection.find(ctx, filter, window)
	if err != nil {
		return result, &operationError{"find", err}
	}
	if cursor == nil {
		return result, ErrValue
	}
	raw := make([]bson.Raw, 0)
	var retained int64
	for cursor.next(ctx) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		document := cursor.current()
		if len(raw) >= r.options.MaxItems || int64(len(raw)) >= window.limit || int64(len(document)) > r.options.MaxBSONBytes-retained {
			return result, ErrLimit
		}
		copy := bson.Raw(bytes.Clone(document))
		if copy.Validate() != nil {
			return result, ErrValue
		}
		raw = append(raw, copy)
		retained += int64(len(copy))
	}
	if err := cursor.err(); err != nil {
		return result, &operationError{"cursor iteration", err}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	data, err := r.materialize(ctx, raw)
	if err != nil {
		return result, err
	}
	return queries.RendererResult[[]T]{Data: data, TotalItems: total}, nil
}

func nonnilFilter(filter bson.D) bson.D {
	if filter == nil {
		return bson.D{}
	}
	return filter
}

func (r *Renderer[T]) freeze(filter bson.D) (bson.Raw, error) {
	var buffer bytes.Buffer
	encoder := bson.NewEncoder(bson.NewDocumentWriter(&buffer))
	encoder.SetRegistry(r.collection.registry)
	if err := encoder.Encode(filter); err != nil {
		return nil, &operationError{"freeze filter", err}
	}
	return bson.Raw(bytes.Clone(buffer.Bytes())), nil
}

func (r *Renderer[T]) sort(s queries.Sorting) (bson.D, error) {
	result := bson.D{}
	if s.Field != "" && s.Direction != queries.Unspecified {
		name, ok := r.collection.sortNames[s.Field]
		if !ok || (s.Direction != queries.Ascending && s.Direction != queries.Descending) {
			return nil, &queries.SortingError{Field: "sortby"}
		}
		direction := int32(1)
		if s.Direction == queries.Descending {
			direction = -1
		}
		result = append(result, bson.E{Key: name, Value: direction})
		if name == "_id" {
			return result, nil
		}
	}
	return append(result, bson.E{Key: "_id", Value: int32(1)}), nil
}

func (r *Renderer[T]) materialize(ctx context.Context, raw []bson.Raw) ([]T, error) {
	if r.collection.options.Ownership == ChronicleOwned {
		identities := make([]bson.RawValue, len(raw))
		for i := range raw {
			identity, err := rawIdentity(raw[i])
			if err != nil {
				return nil, err
			}
			identities[i] = identity
		}
		data, err := r.options.Release(ctx, raw)
		if err != nil {
			return nil, &operationError{"release", err}
		}
		if len(data) != len(raw) {
			return nil, ErrValue
		}
		decodedIdentities := make([]any, 0, len(raw))
		for i := range raw {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Only after release, decode the identity with the declared codec.
			// Protected fields never go through ordinary sink materialization.
			id := reflect.New(r.collection.id.codec.typeOf).Elem()
			if err := identities[i].UnmarshalWithRegistry(r.collection.registry, id.Addr().Interface()); err != nil {
				return nil, ErrValue
			}
			// Distinct BSON IDs can coerce to the same typed identity (for
			// example int32(1)/"1" or binary/string UUIDs). Correspondence is
			// then ambiguous even if each released position appears equal.
			for _, prior := range decodedIdentities {
				if reflect.DeepEqual(prior, id.Interface()) {
					return nil, ErrValue
				}
			}
			decodedIdentities = append(decodedIdentities, id.Interface())
			if !reflect.DeepEqual(id.Interface(), reflect.ValueOf(data[i]).Field(r.collection.id.index).Interface()) {
				return nil, ErrValue
			}
		}
		if data == nil {
			data = []T{}
		}
		return data, nil
	}
	data := make([]T, len(raw))
	for i := range raw {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(raw[i])))
		decoder.SetRegistry(r.collection.registry)
		if err := decoder.Decode(&data[i]); err != nil {
			return nil, &operationError{"decode", err}
		}
	}
	return data, nil
}

func rawIdentity(raw bson.Raw) (bson.RawValue, error) {
	elements, err := raw.Elements()
	if err != nil {
		return bson.RawValue{}, ErrValue
	}
	var identity bson.RawValue
	for _, element := range elements {
		if element.Key() != "_id" {
			continue
		}
		if identity.Type != 0 {
			return bson.RawValue{}, ErrValue
		}
		identity = element.Value()
	}
	if identity.Type == 0 || identity.Type == bson.TypeNull {
		return bson.RawValue{}, ErrValue
	}
	identity.Value = bytes.Clone(identity.Value)
	return identity, nil
}

// Consuming interfaces stay private: applications configure the official client,
// not a provider-specific driver mock or alternate public database API.
type snapshotCollection interface {
	count(context.Context, bson.Raw) (int64, error)
	find(context.Context, bson.Raw, snapshotWindow) (snapshotCursor, error)
}
type snapshotWindow struct {
	sort        bson.D
	skip, limit int64
}
type snapshotCursor interface {
	next(context.Context) bool
	current() bson.Raw
	err() error
	close(context.Context) error
}
type driverCollection struct{ collection *mongo.Collection }

func (d driverCollection) count(ctx context.Context, filter bson.Raw) (int64, error) {
	return d.collection.CountDocuments(ctx, filter, options.Count().SetCollation(&options.Collation{Locale: "simple"}))
}
func (d driverCollection) find(ctx context.Context, filter bson.Raw, w snapshotWindow) (snapshotCursor, error) {
	cursor, err := d.collection.Find(ctx, filter, options.Find().SetCollation(&options.Collation{Locale: "simple"}).SetSort(w.sort).SetSkip(w.skip).SetLimit(w.limit).SetBatchSize(64))
	if cursor == nil {
		return nil, err
	}
	return driverCursor{cursor}, err
}

type driverCursor struct{ cursor *mongo.Cursor }

func (d driverCursor) next(ctx context.Context) bool   { return d.cursor.Next(ctx) }
func (d driverCursor) current() bson.Raw               { return d.cursor.Current }
func (d driverCursor) err() error                      { return d.cursor.Err() }
func (d driverCursor) close(ctx context.Context) error { return d.cursor.Close(ctx) }
func closeCursor(ctx context.Context, cursor snapshotCursor) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &execution.PanicError{Value: value, Stack: debug.Stack()}
		}
	}()
	if err := cursor.close(ctx); err != nil {
		return &operationError{"close cursor", err}
	}
	return ctx.Err()
}

type pagingFailure struct{ findings []validation.Result }

func (*pagingFailure) Error() string                            { return "invalid query parameters" }
func (e *pagingFailure) ValidationResults() []validation.Result { return e.findings }

// Local causes remain inspectable; provider-generated text never embeds values.
type operationError struct {
	operation string
	cause     error
}

func (e *operationError) Error() string { return "MongoDB " + e.operation + " failed" }
func (e *operationError) Unwrap() error { return e.cause }
