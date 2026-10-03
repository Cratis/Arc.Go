// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/identities"
	"github.com/cratis/chronicle.go/metadata"
	"github.com/cratis/chronicle.go/readmodels"
)

type adapter struct {
	client *chronicle.Client
	store  chronicle.StoreName
	events *events.Catalog
	models *readmodels.Catalog
}

// New borrows a client and its frozen selected-store catalogs without connecting.
// The caller coordinates Arc admission, observer startup/drain and client closure.
func New(client *chronicle.Client, config Config) (*integration.Integration, error) {
	if client == nil || strings.TrimSpace(string(config.Store)) == "" {
		return nil, integration.ErrInvalid
	}
	eventCatalog, models, err := client.Catalogs(config.Store)
	if err != nil {
		return nil, err
	}
	a := &adapter{client: client, store: config.Store, events: eventCatalog, models: models}
	return integration.New(integration.Options{StoreResolver: func(_ context.Context, command commands.CommandContext) (integration.Coordinates, error) {
		namespace := chronicle.DefaultNamespace
		if !command.Tenant().IsDefault() {
			namespace = chronicle.Namespace(command.Tenant().String())
		}
		if config.Namespace != nil {
			var err error
			namespace, err = config.Namespace(command.Tenant())
			if err != nil {
				return integration.Coordinates{}, err
			}
		}
		return integration.Coordinates{Store: integration.StoreName(config.Store), Namespace: integration.Namespace(namespace), Sequence: "event-log"}, nil
	}, Transactions: a, Events: a, History: a, Models: a, Appends: a, Concurrency: a, Actor: config.Actor, Audit: config.Audit, SemanticSource: func(value any) (integration.EventSourceID, bool) {
		switch id := value.(type) {
		case events.SourceID:
			return integration.EventSourceID(id), true
		case *events.SourceID:
			if id == nil {
				return "", true
			}
			return integration.EventSourceID(*id), true
		}
		return "", false
	}})
}
func (a *adapter) Descriptors() []integration.EventDescriptor {
	var result []integration.EventDescriptor
	for _, d := range a.events.Descriptors() {
		result = append(result, integration.EventDescriptor{Type: d.GoType(), Identity: integration.EventType{ID: string(d.Ref().ID), Generation: uint32(d.Ref().Generation)}, Validate: func(value any) error { _, err := d.Marshal(value); return err }, Decode: func(body []byte) (any, error) {
			value := reflect.New(d.GoType())
			if err := json.Unmarshal(body, value.Interface()); err != nil {
				return nil, err
			}
			return value.Elem().Interface(), nil
		}})
	}
	return result
}
func (a *adapter) sequence(ctx context.Context, c integration.Coordinates) (*eventsequences.Sequence, error) {
	if string(a.store) != string(c.Store) {
		return nil, integration.ErrMismatch
	}
	store, err := a.client.EventStore(ctx, a.store, chronicle.WithNamespace(chronicle.Namespace(c.Namespace)))
	if err != nil {
		return nil, err
	}
	return store.EventSequence(events.SequenceID(c.Sequence))
}
func auditContext(ctx context.Context) context.Context {
	value := integration.MetadataFrom(ctx)
	actor := identities.Unknown()
	if value.Actor.Subject != "" {
		actor = identities.Identity{Subject: value.Actor.Subject, Name: value.Actor.Name, UserName: value.Actor.UserName}
	}
	ctx = metadata.WithIdentity(ctx, actor)
	// Preserve trusted SDK upstream chain, followed by Arc command causes.
	existing := metadata.CausationChain(ctx)
	common := 0
	for common < len(existing) && common < len(value.Causes) {
		cause := value.Causes[common]
		if !reflect.DeepEqual(existing[common], metadata.Causation{Occurred: cause.Occurred, Type: cause.Type, Properties: cause.Properties}) {
			break
		}
		common++
	}
	for _, cause := range value.Causes[common:] {
		ctx = metadata.WithCausation(ctx, metadata.Causation{Occurred: cause.Occurred, Type: cause.Type, Properties: cause.Properties})
	}
	return ctx
}
