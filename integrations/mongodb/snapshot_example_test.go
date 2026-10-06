// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/xoptions"
)

// The driver's default mock identifies as Single, which intentionally rewrites
// every read preference to primaryPreferred. Use replica-set metadata to record
// the configured primary profile without that unrelated direct-connection rule.
type replicaSetMock struct{ *drivertest.MockDeployment }

func (replicaSetMock) Kind() description.TopologyKind {
	return description.TopologyKindReplicaSetWithPrimary
}

func cursorResponse(namespace string, id int64, batch string, documents ...bson.D) bson.D {
	items := make(bson.A, len(documents))
	for i := range documents {
		items[i] = documents[i]
	}
	return bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.D{{Key: "id", Value: id}, {Key: "ns", Value: namespace}, {Key: batch, Value: items}}}}
}

func TestSnapshotExampleExecutesExternalRegistrationAndDriverProfile(t *testing.T) {
	// The pinned driver's own mock deployment is TEST-ONLY. No socket, live
	// provider, server query semantics or Chronicle release is claimed here.
	deployment := drivertest.NewMockDeployment(
		cursorResponse("Library+Tenant-A.Authors", 0, "firstBatch", bson.D{{Key: "n", Value: int64(2)}}),
		cursorResponse("Library+Tenant-A.Authors", 0, "firstBatch", bson.D{{Key: "_id", Value: int32(2)}, {Key: "Name", Value: "Grace"}, {Key: "Active", Value: true}}),
		bson.D{{Key: "ok", Value: 1}}, bson.D{{Key: "ok", Value: 1}},
	)
	var commands []*event.CommandStartedEvent
	config := options.Client().SetRetryReads(false).SetReadConcern(readconcern.Local()).SetReadPreference(readpref.Secondary()).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		copy := *e
		copy.Command = bytes.Clone(e.Command)
		commands = append(commands, &copy)
	}})
	if err := xoptions.SetInternalClientOptions(config, "deployment", replicaSetMock{deployment}); err != nil {
		t.Fatal(err)
	}
	client, err := mongo.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Disconnect(context.Background()); err != nil {
			t.Error(err)
		}
	})
	tenant, err := tenancy.ParseID("Tenant-A")
	if err != nil {
		t.Fatal(err)
	}
	membershipCalls := 0
	pipeline, err := snapshot.SnapshotExample(client, tenancy.MembershipFunc(func(_ context.Context, p identity.Principal, id tenancy.ID) (bool, error) {
		membershipCalls++
		return p.ID() == "reader" && id == tenant, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatal("construction performed I/O")
	}
	principal := identity.NewPrincipal(identity.PrincipalData{ID: "reader", AuthenticationType: "verified ingress"})
	ctx := tenancy.WithTenant(identity.WithPrincipal(t.Context(), principal), tenant)
	result, err := queries.Perform[[]snapshot.Author](ctx, pipeline, "Author.AllActive", queries.RequestFor(queries.NoArguments{}, queries.Parameters{Paging: queries.Paging{IsPaged: true, Page: 1, Size: 1}, Sorting: queries.Sorting{Field: "Name", Direction: queries.Ascending}}))
	if err != nil {
		t.Fatal(err)
	}
	data, present := result.Data()
	if !present || len(data) != 1 || data[0].ID != 2 || data[0].Name != "Grace" || result.Details().Paging.TotalItems != 2 || result.Details().Paging.TotalPages() != 2 || membershipCalls != 1 {
		t.Fatalf("result %+v data %+v", result.Details(), data)
	}
	if len(commands) != 2 || commands[0].CommandName != "aggregate" || commands[1].CommandName != "find" {
		t.Fatalf("commands %v", commands)
	}
	count, find := commands[0], commands[1]
	match := count.Command.Lookup("pipeline").Array().Index(0).Document().Lookup("$match").Document()
	filter := find.Command.Lookup("filter").Document()
	want, err := bson.Marshal(bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "OwnerID", Value: "reader"}}, bson.D{{Key: "Active", Value: true}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(match, filter) || !bytes.Equal(filter, want) {
		t.Fatal("count/find predicates differ or row authorization absent")
	}
	for _, command := range commands {
		if preference := command.Command.Lookup("$readPreference"); preference.Type != 0 && preference.Document().Lookup("mode").StringValue() != "primary" {
			t.Fatalf("collection preference: %s", preference)
		}
		if command.DatabaseName != "Library+Tenant-A" || command.Command.Lookup("collation").Document().Lookup("locale").StringValue() != "simple" || command.Command.Lookup("readConcern").Document().Lookup("level").StringValue() != "majority" {
			t.Fatalf("profile %s", command.Command)
		}
	}
	if count.Command.Lookup("aggregate").StringValue() != "Authors" || find.Command.Lookup("find").StringValue() != "Authors" || find.Command.Lookup("skip").AsInt64() != 1 || find.Command.Lookup("limit").AsInt64() != 1 || find.Command.Lookup("batchSize").AsInt64() != 64 {
		t.Fatalf("pushdown %s", find.Command)
	}
	if find.Command.Lookup("sort").Document().Lookup("Name").AsInt64() != 1 || find.Command.Lookup("sort").Document().Lookup("_id").AsInt64() != 1 {
		t.Fatal("sort not mapped/deterministic")
	}
	if count.Command.Lookup("skip").Type != 0 || count.Command.Lookup("limit").Type != 0 {
		t.Fatal("windowed count")
	}
	// Denied ingress/membership must not consume the pending wire responses.
	denied := tenancy.WithTenant(t.Context(), tenant)
	failure, err := queries.Perform[[]snapshot.Author](denied, pipeline, "Author.AllActive", queries.Request{})
	if err != nil || failure.IsSuccess() {
		t.Fatalf("anonymous ingress verdict: %v, %v", failure.Details(), err)
	}
	if _, present := failure.Data(); present || len(commands) != 2 {
		t.Fatal("denial issued MongoDB operations")
	}
	// Arc's operation resource cleanup must not disconnect this borrowed client.
	if err := client.Ping(t.Context(), nil); err != nil {
		t.Fatal("borrowed client was closed:", err)
	}
}
