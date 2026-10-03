// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/xoptions"
)

func TestWatchDriverUsesOrdinaryAggregateCursorWithoutResume(t *testing.T) {
	// Pinned driver's test-only wire deployment: no socket or provider evidence.
	deployment := drivertest.NewMockDeployment(
		bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.D{{Key: "id", Value: int64(123)}, {Key: "ns", Value: "Library.$cmd.aggregate"}, {Key: "firstBatch", Value: bson.A{}}}}},
		bson.D{{Key: "ok", Value: 0}, {Key: "code", Value: 286}, {Key: "errmsg", Value: "history lost"}},
		bson.D{{Key: "ok", Value: 1}},
		bson.D{{Key: "ok", Value: 1}},
	)
	var mu sync.Mutex
	var commands []bson.Raw
	config := options.Client().SetRetryReads(false).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		mu.Lock()
		commands = append(commands, bson.Raw(bytes.Clone(e.Command)))
		mu.Unlock()
	}})
	if err := xoptions.SetInternalClientOptions(config, "deployment", deployment); err != nil {
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
	w, err := NewWatcher(context.Background(), client, WatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := testObserve(t, w, testWatchBinding[author](t, client, "Library", "Authors"))
	stream, _ := source.Open(context.Background())
	if stream == nil {
		t.Fatal("source did not transfer its subscriber")
	}
	w.mu.Lock()
	done := w.databases["Library"].done
	w.mu.Unlock()
	<-done
	if _, err := stream.Next(context.Background()); !errors.Is(err, ErrResnapshotRequired) {
		t.Fatalf("ordinary cursor loss was resumed or hidden: %v", err)
	}
	testWatchClose(t, stream)
	testWatchClose(t, w)
	if err := client.Ping(context.Background(), nil); err != nil {
		t.Fatalf("borrowed client disconnected: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(commands) != 4 {
		t.Fatalf("unexpected resume/retry commands: %v", commands)
	}
	pipeline := commands[0].Lookup("pipeline").Array()
	stages, err := pipeline.Values()
	if err != nil || len(stages) != 2 {
		t.Fatalf("watch pipeline: %v %v", stages, err)
	}
	change := stages[0].Document().Lookup("$changeStream").Document()
	if len(change) != 5 {
		t.Fatalf("unexpected full-document/resume options: %v", change)
	}
	projection := stages[1].Document().Lookup("$project").Document()
	if projection.Lookup("operationType").Type == 0 || projection.Lookup("ns").Type == 0 || projection.Lookup("fullDocument").Type != 0 {
		t.Fatal("read-model payload retained by watch projection")
	}
	if commands[0].Lookup("aggregate").Int32() != 1 || commands[0].Lookup("cursor", "batchSize").Int32() != 64 || commands[1].Lookup("getMore").Int64() != 123 || commands[2].Lookup("killCursors").Type == 0 || commands[3].Lookup("ping").Type == 0 {
		t.Fatalf("ordinary cursor/batch/cleanup profile: %v", commands)
	}
}
