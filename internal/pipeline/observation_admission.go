// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline

import (
	"context"
	"sync"
)

type observationAdmissionKey struct{}

// ObservationAdmission forwards one root application lifetime lease. It is never
// a query authorization verdict. Only an observation that was actually retained
// can take ownership; early failure leaves the caller responsible for release.
type ObservationAdmission struct {
	mu      sync.Mutex
	release func()
	taken   bool
}

// WithObservationAdmission creates a one-use lease for an admitted Open call.
func WithObservationAdmission(ctx context.Context, release func()) (context.Context, *ObservationAdmission) {
	lease := &ObservationAdmission{release: release}
	return context.WithValue(ctx, observationAdmissionKey{}, lease), lease
}

// TakeObservationAdmission transfers release ownership to a registered observation.
func TakeObservationAdmission(ctx context.Context) func() {
	lease, _ := ctx.Value(observationAdmissionKey{}).(*ObservationAdmission)
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.taken {
		return nil
	}
	lease.taken = true
	release := lease.release
	lease.release = nil
	return release
}

// ReleaseUnused releases an untaken lease exactly once. Transferred leases are
// released only by successful final Close, including failed-opening cleanup.
func (l *ObservationAdmission) ReleaseUnused() {
	l.mu.Lock()
	release := l.release
	l.release = nil
	l.mu.Unlock()
	if release != nil {
		release()
	}
}

type observationProbeKey struct{}

// WithObservationProbe requests admission without performer/source activation.
// HEAD uses it; wait controls cannot turn a probe into a subscription.
func WithObservationProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, observationProbeKey{}, true)
}

// IsObservationProbe checks a framework-owned nonactivating query probe.
func IsObservationProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(observationProbeKey{}).(bool)
	return value
}
