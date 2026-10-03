// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/internal/httptransport"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/queries"
)

func (a *Application) hubSSEEndpoint(w http.ResponseWriter, r *http.Request) {
	if !a.hubOriginAllowed(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	a.expireHubCookies(w, r)
	if r.Method == "HEAD" {
		w.WriteHeader(http.StatusOK)
		return
	}
	id, err := opaqueHubID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	owner, evidence, err := a.hubOwner(r, id, true)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	sse, err := streaming.NewSSEWriter(w, r.ProtoMajor, a.options.Observable.WriteTimeout)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.options.Observable.ConnectionLifetime)
	defer cancel()
	subscriptions, err := streaming.NewSubscriptions(ctx, streaming.SubscriptionOptions{MaxIDs: a.options.Observable.MaxQueryIDs, MaxOperations: a.options.Observable.MaxSubscriptions})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	principal, _ := identity.PrincipalFrom(r.Context())
	c := &hubConnection{frameOverhead: 8, id: id, peer: actualPeer(r), anonymous: !principal.IsAuthenticated(), owner: owner, ctx: ctx, cancel: cancel, subscriptions: subscriptions, workers: map[*streaming.Operation]*hubWorker{}, joinGate: make(chan struct{}, 1), connected: make(chan struct{})}
	c.writer, err = streaming.NewWriter(ctx, streaming.WriterOptions{
		MaxJobs: a.options.Observable.MaxOutboundJobs, MaxFrameBytes: a.options.HTTP.MaxResponseBytes,
		MaxQueuedBytes: a.options.Observable.MaxQueuedBytes, Application: a.hubs.budget, Owners: subscriptions,
		KeepAliveInterval: a.options.Observable.KeepAliveInterval,
		KeepAlive: func(ctx context.Context) error {
			select {
			case <-c.connected:
			default:
				return nil
			}
			timestamp := a.options.Clock().UnixMilli()
			body, err := encode(ctx, streaming.Message{Type: "Ping", Timestamp: &timestamp})
			if err != nil {
				return err
			}
			if int64(len(body))+8 > a.options.HTTP.MaxResponseBytes {
				return streaming.ErrFrameCapacity
			}
			return sse.Write(ctx, body)
		},
	}, sse.Write)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := a.registerHub(c); err != nil {
		// Construction starts no worker; close before returning to release state.
		_ = c.writer.Close(ctx)
		w.WriteHeader(hubControlStatus(err))
		return
	}
	defer func() {
		cleanup, stop, err := boundary.CleanupContext(ctx, a.options.Observable.CloseGrace)
		if err == nil {
			defer stop()
			err = a.closeHub(cleanup, c)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			a.hostFailure(ctx, "observable hub cleanup failed", err)
		}
		// ResponseWriter must not outlive ServeHTTP, even if cleanup times out.
		// Actual writes have deadlines; Shutdown can still return its own timeout.
		<-c.writer.Done()
	}()
	a.setHubCookie(w, r, id, evidence)
	if err := sse.Start(); err != nil {
		_ = c.writer.Close(ctx)
		return
	}
	go func() {
		if err := c.writer.Run(); err != nil && !errors.Is(err, context.Canceled) {
			a.hostFailure(ctx, "observable hub writer failed", err)
		}
	}()
	if err := a.sendHub(c, nil, streaming.Message{Type: "Connected", Payload: id, KeepAliveIntervalMs: a.options.Observable.KeepAliveInterval.Milliseconds(), SupportsSubscriptionRevisions: true}, false); err != nil {
		return
	}
	close(c.connected)
	select {
	case <-ctx.Done():
	case <-c.writer.Done():
	}
}

func hubControlStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, streaming.ErrCapacity), errors.Is(err, queries.ErrObservationCapacity):
		return http.StatusTooManyRequests
	case errors.Is(err, streaming.ErrDraining), errors.Is(err, queries.ErrObservationsStopping), errors.Is(err, ErrShuttingDown), errors.Is(err, ErrStopped):
		return http.StatusServiceUnavailable
	case errors.Is(err, streaming.ErrControl), errors.Is(err, streaming.ErrRevision):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
func (a *Application) hubSSEControl(w http.ResponseWriter, r *http.Request, subscribe bool) {
	if !a.hubOriginAllowed(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	a.expireHubCookies(w, r)
	body, status, err := httptransport.ReadBody(w, r, a.options.HTTP.MaxBodyBytes)
	if err != nil {
		w.WriteHeader(status)
		return
	}
	control, err := streaming.ParseSSEControl(body, subscribe)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	owner, _, err := a.hubOwner(r, control.ConnectionID, false)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	c := a.resolveHub(control.ConnectionID, owner)
	if c == nil || c.websocket {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !subscribe {
		w.WriteHeader(hubControlStatus(c.subscriptions.Unsubscribe(control.QueryID, control.Revision)))
		return
	}
	operation, replaced, worker, err := a.reserveHub(c, control)
	if err != nil || operation == nil {
		w.WriteHeader(hubControlStatus(err))
		return
	}
	ctx := hubSubscriptionContext(operation.Context(), r.Context())
	admitted := make(chan int, 1)
	// The GET connection retains and joins the worker. POST cancellation affects
	// only waiting for admission; it never lends its resources to the worker.
	go a.runHubSubscription(c, operation, replaced, worker, ctx, *control.Request, admitted)
	select {
	case status := <-admitted:
		w.WriteHeader(status)
	case <-r.Context().Done():
		return
	}
}
func hubRequest(input streaming.SubscriptionRequest) (queries.Request, queries.TransferMode, error) {
	var parameters queries.Parameters
	if input.PageSize != nil {
		parameters.Paging = queries.Paging{Size: queries.PageSize(*input.PageSize), IsPaged: true}
		if input.Page != nil {
			parameters.Paging.Page = queries.PageNumber(*input.Page)
		}
	}
	// The hub differs from QUERY: both sorting fields must be supplied, and
	// every non-desc textual direction selects ascending in the reference.
	if input.SortBy != nil && *input.SortBy != "" && input.SortDirection != nil && *input.SortDirection != "" {
		direction := queries.Ascending
		if strings.EqualFold(*input.SortDirection, "desc") {
			direction = queries.Descending
		}
		parameters.Sorting = queries.Sorting{Field: queries.SortField(*input.SortBy), Direction: direction}
	}
	mode := queries.Legacy
	if input.TransferMode != nil {
		switch strings.ToLower(*input.TransferMode) {
		case "full":
			mode = queries.Full
		case "delta":
			mode = queries.Delta
		}
	}
	request, err := queries.ReadHub(input.Arguments, parameters)
	return request, mode, err
}
func (a *Application) runHubSubscription(c *hubConnection, operation, replaced *streaming.Operation, worker *hubWorker, ctx context.Context, input streaming.SubscriptionRequest, admitted chan<- int) {
	opening := true
	defer func() {
		if opening {
			a.hubOpeningFinished(c)
		}
		var cleanupErr error
		if worker.observation != nil {
			cleanup, cancel, err := boundary.CleanupContext(ctx, a.options.Observable.CloseGrace)
			if err == nil {
				cleanupErr = worker.observation.Close(cleanup)
				cancel()
			} else {
				cleanupErr = err
			}
		}
		if cleanupErr != nil {
			a.hostFailure(ctx, "observable hub subscription cleanup failed", cleanupErr)
		}
		joined := joinedHubCleanup(cleanupErr)
		if worker.cleanupJoined != nil {
			select {
			case <-worker.cleanupJoined:
			default:
				joined = false
			}
		}
		close(worker.done)
		if joined {
			a.hubJoined(c, operation)
		}
	}()
	// Cookie names can reveal an ID before Connected is flushed. Early controls
	// may reserve bounded state, but source activation waits for its acknowledgement.
	select {
	case <-c.connected:
	case <-ctx.Done():
		admitted <- http.StatusOK
		return
	}
	// Legacy frames already in flight cannot be retracted. Fence before invoking
	// the successor source so no old legacy frame trails its initial snapshot.
	if replaced != nil && replaced.Revision() == nil {
		if err := c.writer.Fence(operation.Context()); err != nil {
			admitted <- http.StatusOK
			return
		}
	}
	if !c.subscriptions.Owns(operation) {
		admitted <- http.StatusOK
		return
	}
	request, mode, err := hubRequest(input)
	if err != nil {
		admitted <- http.StatusBadRequest
		return
	}
	// Delta/legacy are explicitly rejected until the delivered-baseline slice,
	// never silently downgraded. This checkpoint supports full hub transfers.
	if mode != queries.Full {
		admitted <- http.StatusOK
		_ = a.sendHub(c, operation, streaming.Message{Type: "Error", Payload: "Observable collection transfer mode is not supported."}, true)
		return
	}
	p, ok := a.Queries().(queries.ObservablePipeline)
	if !ok {
		admitted <- http.StatusInternalServerError
		return
	}
	joined := make(chan struct{})
	worker.cleanupJoined = joined
	openContext, lease := boundary.WithObservationAdmission(ctx, func() { close(joined) })
	var admission queries.Result[any]
	worker.observation, admission, err = p.Open(openContext, queries.FullyQualifiedQueryName(input.QueryName), request)
	lease.ReleaseUnused()
	a.hubOpeningFinished(c)
	opening = false
	if worker.observation == nil {
		if !admission.Details().Authorized {
			admitted <- http.StatusUnauthorized
			_ = a.sendHub(c, operation, streaming.Message{Type: "Unauthorized"}, true)
		} else {
			status := hubControlStatus(err)
			if status == http.StatusInternalServerError {
				status = http.StatusOK
			}
			admitted <- status
			_ = a.sendHub(c, operation, streaming.Message{Type: "Error", Payload: "Unable to open observable query."}, true)
		}
		return
	}
	admitted <- http.StatusOK
	err = worker.observation.Run(ctx, queries.ObservationOptions{TransferMode: mode}, func(result queries.Result[any]) error {
		if !result.Details().Authorized {
			return a.sendHub(c, operation, streaming.Message{Type: "Unauthorized"}, true)
		}
		if result.HasExceptions() {
			return a.sendHub(c, operation, streaming.Message{Type: "Error", Payload: "Unable to deliver observable query result."}, true)
		}
		return a.sendHub(c, operation, streaming.Message{Type: "QueryResult", Payload: result}, false)
	})
	if err != nil && ctx.Err() == nil && !errors.Is(err, streaming.ErrObsolete) && !errors.Is(err, streaming.ErrWriterClosed) {
		a.hostFailure(ctx, "observable hub subscription failed", err)
	}
}
func (a *Application) sendHub(c *hubConnection, operation *streaming.Operation, message streaming.Message, terminal bool) error {
	ctx := c.ctx
	if operation != nil {
		ctx = operation.Context()
		message.QueryID, message.Revision = operation.QueryID(), operation.Revision()
	}
	body, err := encode(ctx, message)
	if err != nil || int64(len(body))+c.frameOverhead > a.options.HTTP.MaxResponseBytes {
		if err == nil {
			err = streaming.ErrFrameCapacity
		}
		if operation != nil {
			fallback, fallbackErr := encode(ctx, streaming.Message{Type: "Error", QueryID: operation.QueryID(), Revision: operation.Revision(), Payload: "Unable to deliver observable query result."})
			if fallbackErr == nil && int64(len(fallback))+c.frameOverhead <= a.options.HTTP.MaxResponseBytes {
				fallbackErr = c.writer.DeliverTerminal(ctx, fallback, operation)
			}
			return errors.Join(err, fallbackErr)
		}
		return err
	}
	if terminal {
		return c.writer.DeliverTerminal(ctx, body, operation)
	}
	return c.writer.Deliver(ctx, body, operation)
}
