// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package observability records bounded, payload-free backend diagnostics.
// Recording never calls application code or starts workers. Applications own
// synchronous export through Drain; a blocked exporter blocks only its caller.
// Attempts, stream consumption, acknowledged data and actual cleanup joins are
// separate phases. Delivered updates cumulative metrics only, not the event ring.
package observability

import (
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
)

// Other is the label for unknown and over-capacity artifacts.
const Other = "_other"

// Operation identifies a fixed backend operation.
type Operation uint8

const (
	// Command executes a command.
	Command Operation = iota
	// Validate validates a command without executing it.
	Validate
	// Query performs a query.
	Query
)

// Transport distinguishes snapshot and observable query execution.
type Transport uint8

const (
	// Unknown is a non-query or unspecified transport.
	Unknown Transport = iota
	// SnapshotTransport performs a unary query.
	SnapshotTransport
	// ObservableTransport opens an observation.
	ObservableTransport
)

// Phase identifies the measured lifecycle boundary.
type Phase uint8

const (
	// Completed records a finalized snapshot or command invocation.
	Completed Phase = iota
	// Opening records observable admission and source activation.
	Opening
	// Consumption records a single consumer's terminal outcome before Close cancels.
	Consumption
	// FirstDelivery records the first successfully acknowledged data result.
	FirstDelivery
	// Delivered counts every acknowledged data result, without queuing events.
	Delivered
	// Joined records lifetime through actual release of source and resource ownership.
	Joined
	// Cleanup records elapsed time from the first Close until actual ownership release.
	Cleanup
)

// Outcome is a fixed, payload-free final classification.
type Outcome uint8

const (
	// Success means the measured phase completed normally, not proof of commit.
	Success Outcome = iota
	// Validation means retained input or business validation findings.
	Validation
	// Authorization means access was denied, taking precedence over other failures.
	Authorization
	// AppendRejected means a constraint or concurrency validation reason.
	AppendRejected
	// Cancelled means a failed operation whose execution context was cancelled.
	Cancelled
	// Error means another failure. No error value or message is retained.
	Error
)

// String returns the stable telemetry spelling, including for unknown values.
func (o Outcome) String() string {
	switch o {
	case Success:
		return "success"
	case Validation:
		return "validation"
	case Authorization:
		return "authorization"
	case AppendRejected:
		return "append_rejected"
	case Cancelled:
		return "cancelled"
	default:
		return "error"
	}
}

// Options bounds recorder-wide memory, even when pipelines share a recorder.
type Options struct {
	// ArtifactLimit defaults to 1000, the maximum; _other does not use a slot.
	ArtifactLimit int
	// EventCapacity defaults to 256. Its maximum is 65536.
	EventCapacity int
}

// Observation contains copied scalars only; it cannot carry a payload or error.
type Observation struct {
	// Artifact is a registered label, or _other.
	Artifact string
	// Operation is the backend operation.
	Operation Operation
	// Transport is the query delivery classification.
	Transport Transport
	// Phase identifies the completed lifecycle boundary.
	Phase Phase
	// Outcome is the final result classification.
	Outcome Outcome
	// Seconds is monotonic elapsed time in seconds.
	Seconds float64
}

type key struct {
	artifact  string
	operation Operation
	transport Transport
	phase     Phase
	outcome   Outcome
}

// Metric is a cumulative count and duration sum independent of event retention.
type Metric struct {
	// Observation contains labels and the cumulative seconds for this series.
	Observation
	// Count is the cumulative number of observations.
	Count uint64
}

// Snapshot is detached recorder state. Mutations never affect the recorder.
type Snapshot struct {
	// Metrics are cumulative series in stable label order.
	Metrics []Metric
	// Events are queued observations in recording order.
	Events []Observation
	// Dropped counts events discarded because the ring was full.
	Dropped uint64
	// ExportErrors counts failed synchronous exporter calls.
	ExportErrors uint64
	// ExportPanics counts recovered exporter panics, without retaining values.
	ExportPanics uint64
}

// DrainReport describes one batch, removed before callbacks run and never retried.
type DrainReport struct {
	// Removed is the number of dequeued events.
	Removed int
	// Errors is the number of callbacks returning an error.
	Errors int
	// Panics is the number of recovered callback panics.
	Panics int
}

// Recorder is concurrent-safe and must not be copied. Use NewRecorder; the zero
// value and a nil pointer are disabled. No producer waits for exporter callbacks.
type Recorder struct {
	mu                                  sync.Mutex
	limit                               int
	labels                              map[string]struct{}
	metrics                             map[key]Metric
	events                              []Observation
	start, count                        int
	dropped, exportErrors, exportPanics uint64
}

// NewRecorder constructs bounded state without I/O, callbacks or goroutines.
func NewRecorder(options Options) (*Recorder, error) {
	if options.ArtifactLimit < 0 || options.ArtifactLimit > 1000 || options.EventCapacity < 0 || options.EventCapacity > 65536 {
		return nil, errors.New("invalid diagnostics capacity")
	}
	if options.ArtifactLimit == 0 {
		options.ArtifactLimit = 1000
	}
	if options.EventCapacity == 0 {
		options.EventCapacity = 256
	}
	return &Recorder{limit: options.ArtifactLimit, labels: make(map[string]struct{}), metrics: make(map[key]Metric), events: make([]Observation, options.EventCapacity)}, nil
}

// Register admits frozen artifact identities, sorted within this batch. Earlier
// batches win recorder-wide capacity. Call only with trusted registration names,
// never request input. Empty, reserved and over-512-byte names use _other.
// Rejected identities are not retained. It activates no application services.
func (r *Recorder) Register(names []string) {
	if r == nil {
		return
	}
	names = slices.Clone(names)
	slices.Sort(names)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		if len(r.labels) >= r.limit {
			break
		}
		if name == "" || name == Other || len(name) > 512 {
			continue
		}
		r.labels[strings.Clone(name)] = struct{}{}
	}
}

// Label returns a registered identity or _other without retaining unknown input.
func (r *Recorder) Label(name string) string {
	if r == nil {
		return Other
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.labels[name]; ok {
		return strings.Clone(name)
	}
	return Other
}

// Record updates cumulative metrics and a bounded event ring. Only trusted
// adapters should produce observations. Unknown labels/enums are normalized;
// nonfinite or negative durations become zero. Full rings drop new events.
func (r *Recorder) Record(event Observation) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.limit == 0 {
		return
	}
	if _, ok := r.labels[event.Artifact]; !ok {
		event.Artifact = Other
	} else {
		event.Artifact = strings.Clone(event.Artifact)
	}
	if event.Operation > Query {
		event.Operation = Query
	}
	if event.Transport > ObservableTransport {
		event.Transport = Unknown
	}
	if event.Phase > Cleanup {
		event.Phase = Completed
	}
	if event.Outcome > Error {
		event.Outcome = Error
	}
	if event.Seconds < 0 || math.IsNaN(event.Seconds) || math.IsInf(event.Seconds, 0) {
		event.Seconds = 0
	}
	k := key{event.Artifact, event.Operation, event.Transport, event.Phase, event.Outcome}
	metric := r.metrics[k]
	seconds := metric.Seconds + event.Seconds
	if math.IsInf(seconds, 0) {
		seconds = math.MaxFloat64
	}
	metric.Observation = event
	metric.Seconds = seconds
	metric.Count++
	r.metrics[k] = metric
	if event.Phase == Delivered {
		return
	}
	if r.count == len(r.events) {
		r.dropped++
		return
	}
	r.events[(r.start+r.count)%len(r.events)] = event
	r.count++
}

// Snapshot copies bounded state under the recorder lock, never calling exporters.
func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := Snapshot{Dropped: r.dropped, ExportErrors: r.exportErrors, ExportPanics: r.exportPanics}
	for _, metric := range r.metrics {
		snapshot.Metrics = append(snapshot.Metrics, metric)
	}
	slices.SortFunc(snapshot.Metrics, func(a, b Metric) int {
		if n := strings.Compare(a.Artifact, b.Artifact); n != 0 {
			return n
		}
		if a.Operation != b.Operation {
			return int(a.Operation) - int(b.Operation)
		}
		if a.Transport != b.Transport {
			return int(a.Transport) - int(b.Transport)
		}
		if a.Phase != b.Phase {
			return int(a.Phase) - int(b.Phase)
		}
		return int(a.Outcome) - int(b.Outcome)
	})
	for i := 0; i < r.count; i++ {
		snapshot.Events = append(snapshot.Events, r.events[(r.start+i)%len(r.events)])
	}
	return snapshot
}

// Drain removes at most limit events, unlocks, then synchronously invokes observe.
// Nonpositive limits or nil callbacks remove nothing. Errors and panics are counted
// without retaining their values; successful and failed exports are never retried.
// Callbacks may reenter the recorder. Concurrent drains own disjoint batches but
// may invoke callbacks concurrently/out of order. A blocked callback blocks Drain;
// no timeout goroutine is created. Applications must own cancellation/export I/O.
func (r *Recorder) Drain(limit int, observe func(Observation) error) DrainReport {
	if r == nil || limit <= 0 || observe == nil {
		return DrainReport{}
	}
	r.mu.Lock()
	n := min(limit, r.count)
	batch := make([]Observation, n)
	for i := range batch {
		batch[i] = r.events[r.start]
		r.events[r.start] = Observation{}
		r.start = (r.start + 1) % len(r.events)
	}
	r.count -= n
	r.mu.Unlock()
	report := DrainReport{Removed: n}
	for _, event := range batch {
		func() {
			returned := false
			defer func() {
				if !returned {
					_ = recover()
					report.Panics++
				}
			}()
			if observe(event) != nil {
				report.Errors++
			}
			returned = true
		}()
	}
	r.mu.Lock()
	r.exportErrors += uint64(report.Errors)
	r.exportPanics += uint64(report.Panics)
	r.mu.Unlock()
	return report
}
