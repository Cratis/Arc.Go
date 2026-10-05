//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"net/http"
	"testing"
)

// These are two independent Arc applications behind real loopback HTTP routing,
// not two OS processes or a browser/load-balancer deployment certification. Each
// consumes the Mongo module's fetchable root pin with GOWORK=off. No broker,
// shared in-memory source, distributed hub registry or local root replace is used.
func TestLiveTwoInstanceProviderBroadcastAndIsolation(t *testing.T) {
	const collection = "TwoInstanceDirect"
	first := newTwoInstanceNode(t, collection)
	second := newTwoInstanceNode(t, collection)
	if first.provider.client == second.provider.client || first.watcher == second.watcher || first.app == second.app {
		t.Fatal("instances must not share application/provider owners")
	}
	router := twoInstanceRouter(t, first, second)
	a, b := first.provider.a, first.provider.b
	readerA := providerTask{ID: 1, Name: "tenant A reader", Active: true}
	otherA := providerTask{ID: 2, Name: "tenant A other", Active: true}
	readerB := providerTask{ID: 1, Name: "tenant B reader", Active: true}
	insertRows(t, first.provider.collection(t, a, collection), []any{
		twoInstanceRow(readerA.ID, readerA.Name, "reader"),
		twoInstanceRow(otherA.ID, otherA.Name, "other"),
	})
	insertRows(t, first.provider.collection(t, b, collection), []any{twoInstanceRow(readerB.ID, readerB.Name, "reader")})

	type tenantStreams struct{ readerA, otherA, readerB *twoInstanceStream }
	var streams []tenantStreams
	for _, instance := range []string{"first", "second"} {
		readers := tenantStreams{
			readerA: openTwoInstanceStream(t, router, "/observe?transferMode=full", twoInstanceHeaders(instance, "reader", a)),
			otherA:  openTwoInstanceStream(t, router, "/observe?transferMode=full", twoInstanceHeaders(instance, "other", a)),
			readerB: openTwoInstanceStream(t, router, "/observe?transferMode=full", twoInstanceHeaders(instance, "reader", b)),
		}
		readers.readerA.full(t, readerA)
		readers.otherA.full(t, otherA)
		readers.readerB.full(t, readerB)
		streams = append(streams, readers)
	}
	// Both applications independently established both tenant database watches
	// before the mutation. Multiple principals share a database reader, not rows.
	assertLiveWatchProfile(t, first.provider.record, 2)
	assertLiveWatchProfile(t, second.provider.record, 2)
	readerA.Name = "tenant A changed"
	changeTwoInstanceRow(t, first, a, collection, readerA)
	for _, readers := range streams {
		readers.readerA.full(t, readerA)
		// The same real database marker reaches the other principal. Its authorized
		// refetch remains its own row; isolation is asserted on delivered data.
		readers.otherA.full(t, otherA)
	}
	readerB.Name = "tenant B changed"
	changeTwoInstanceRow(t, first, b, collection, readerB)
	for _, readers := range streams {
		// A tenant-A marker incorrectly routed to B would leave a stale B frame
		// ahead of this one and fail; no guessed silence interval is used.
		readers.readerB.full(t, readerB)
	}

	for index, node := range []*twoInstanceNode{first, second} {
		assertTwoInstanceWatchersJoined(t, node, 2)
		streams[index].readerA.ended(t, false)
		streams[index].otherA.ended(t, false)
		streams[index].readerB.ended(t, false)
	}
}

func TestLiveTwoInstanceHubAffinityLossAndExplicitBaselineReset(t *testing.T) {
	const collection = "TwoInstanceHub"
	first := newTwoInstanceNode(t, collection)
	second := newTwoInstanceNode(t, collection)
	router := twoInstanceRouter(t, first, second)
	a, b := first.provider.a, first.provider.b
	row := providerTask{ID: 1, Name: "before loss", Active: true}
	insertRows(t, first.provider.collection(t, a, collection), []any{twoInstanceRow(row.ID, row.Name, "reader")})
	firstOwner := twoInstanceHeaders("first", "reader", a)
	secondOwner := twoInstanceHeaders("second", "reader", a)
	firstHub := openTwoInstanceStream(t, router, twoInstanceHubPath, firstOwner)
	secondHub := openTwoInstanceStream(t, router, twoInstanceHubPath, secondOwner)
	if firstHub.id == secondHub.id {
		t.Fatal("independent connections reused an ID")
	}
	for index, hub := range []*twoInstanceStream{firstHub, secondHub} {
		headers := []http.Header{firstOwner, secondOwner}[index]
		for _, queryID := range []string{"one", "two"} {
			twoInstanceControl(t, router, headers, hub.id, "subscribe", queryID, 1, http.StatusOK)
		}
		hub.hubResults(t, 1, true, row, "one", "two")
	}
	direct := openTwoInstanceStream(t, router, "/observe?transferMode=full", firstOwner)
	direct.full(t, row)
	assertLiveWatchProfile(t, first.provider.record, 1)
	assertLiveWatchProfile(t, second.provider.record, 1)
	beforeFirst, beforeSecond := first.opened.Load(), second.opened.Load()
	for _, action := range []string{"subscribe", "unsubscribe"} {
		for _, headers := range []http.Header{
			secondOwner, // Correct owner, wrong instance: no distributed registry.
			twoInstanceHeaders("first", "other", a),
			twoInstanceHeaders("first", "reader", b),
			twoInstanceHeaders("first", "", a),
		} {
			twoInstanceControl(t, router, headers, firstHub.id, action, "one", 1, http.StatusNotFound)
		}
		// Affinity is symmetric, not a special first-instance fallback.
		twoInstanceControl(t, router, firstOwner, secondHub.id, action, "one", 1, http.StatusNotFound)
	}
	if first.opened.Load() != beforeFirst || second.opened.Load() != beforeSecond {
		t.Fatal("foreign/wrong-instance controls opened query resources")
	}
	row.Name = "both instances observe independently"
	changeTwoInstanceRow(t, first, a, collection, row)
	direct.full(t, row)
	firstHub.hubResults(t, 1, false, row, "one", "two")
	secondHub.hubResults(t, 1, false, row, "one", "two")

	// Lose the first instance's actual sockets while subscriptions are active,
	// then join its application/provider owners. This is fail-stop transport loss,
	// not a claim about OS process crashes, Mongo failover or transparent resume.
	first.server.CloseClientConnections()
	assertTwoInstanceWatchersJoined(t, first, 1)
	firstHub.ended(t, true)
	direct.ended(t, true)
	for _, action := range []string{"subscribe", "unsubscribe"} {
		twoInstanceControl(t, router, secondOwner, firstHub.id, action, "one", 1, http.StatusNotFound)
	}
	// The surviving instance remains live while the lost client has no stream.
	row.Name = "changed while disconnected"
	changeTwoInstanceRow(t, second, a, collection, row)
	secondHub.hubResults(t, 1, false, row, "one", "two")

	// Recovery is an explicit new physical connection and subscription, not a
	// continuation token. Reusing a query ID/revision must still send full data.
	reconnected := openTwoInstanceStream(t, router, twoInstanceHubPath, secondOwner)
	if reconnected.id == firstHub.id || reconnected.id == secondHub.id {
		t.Fatal("reconnect did not create fresh ownership")
	}
	twoInstanceControl(t, router, secondOwner, reconnected.id, "subscribe", "one", 1, http.StatusOK)
	reconnected.hubResults(t, 1, true, row, "one")
	reopenedDirect := openTwoInstanceStream(t, router, "/observe?transferMode=full", secondOwner)
	reopenedDirect.full(t, row)

	// A correct-owner unsubscribe/resubscribe on the same connection also resets
	// its delta baseline, independently of the survivor's two original queries.
	twoInstanceControl(t, router, secondOwner, reconnected.id, "unsubscribe", "one", 1, http.StatusOK)
	twoInstanceControl(t, router, secondOwner, reconnected.id, "subscribe", "one", 2, http.StatusOK)
	reconnected.hubResults(t, 2, true, row, "one")
	row.Name = "after explicit reset"
	changeTwoInstanceRow(t, second, a, collection, row)
	secondHub.hubResults(t, 1, false, row, "one", "two")
	reconnected.hubResults(t, 2, false, row, "one")
	reopenedDirect.full(t, row)
	assertTwoInstanceWatchersJoined(t, second, 1)
	secondHub.ended(t, false)
	reconnected.ended(t, false)
	reopenedDirect.ended(t, false)
}
