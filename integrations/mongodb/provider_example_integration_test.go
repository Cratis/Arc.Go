//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestLiveManualHTTPExampleUsesRealListenerProviderAndBorrowedResources(t *testing.T) {
	f := liveProvider(t)
	tenant, err := tenancy.ParseID(osOwner() + "A")
	if err != nil {
		t.Fatal(err)
	}
	database, err := mongodb.DatabaseName("Library", tenant)
	if err != nil {
		t.Fatal(err)
	}
	collection := f.client.Database(database).Collection("Authors")
	insertRows(t, collection, []any{
		bson.D{{Key: "_id", Value: int32(1)}, {Key: "Name", Value: "Ada"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}},
		bson.D{{Key: "_id", Value: int32(2)}, {Key: "Name", Value: "Grace"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}},
		bson.D{{Key: "_id", Value: int32(3)}, {Key: "Name", Value: "Forbidden"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "other"}},
	})
	resolver, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Fixed, FixedID: tenant})
	if err != nil {
		t.Fatal(err)
	}
	app, err := snapshot.SnapshotHTTPExample(f.client, []authentication.Handler{providerAuthentication()}, resolver, providerMembership(tenant, tenant))
	if err != nil {
		t.Fatal(err)
	}
	startProviderApp(t, app)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 3 * time.Second}
	perform := func(authenticated bool) (int, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/authors?page=1&pageSize=1&sortby=Name&sortDirection=asc", nil)
		if err != nil {
			t.Fatal(err)
		}
		if authenticated {
			request.Header.Set("Authorization", "Bearer provider-fixture-reader")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	f.record.reset()
	status, body := perform(true)
	var envelope struct {
		Data   []snapshot.Author `json:"data"`
		Paging struct {
			Total int64 `json:"totalItems"`
		} `json:"paging"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if status != 200 || len(envelope.Data) != 1 || envelope.Data[0].ID != 2 || envelope.Paging.Total != 2 {
		t.Fatal("manual HTTP example", status, string(body))
	}
	assertLivePushdown(t, f.record.snapshot(), database, "Authors", 1, 1)
	f.record.reset()
	status, body = perform(false)
	if status != 403 || len(f.record.snapshot()) != 0 {
		t.Fatal("denied manual example performed I/O", status, string(body))
	}
	if err := f.client.Ping(context.WithoutCancel(t.Context()), nil); err != nil {
		t.Fatal("manual example closed borrowed client", err)
	}
}
