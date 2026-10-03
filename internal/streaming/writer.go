// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	boundary "github.com/cratis/arc.go/internal/pipeline"
)

var (
	// ErrWriterCapacity closes a slow connection rather than silently dropping data.
	ErrWriterCapacity = errors.New("streaming outbound job capacity exhausted")
	// ErrFrameCapacity rejects oversized encoded frames before copying or writing.
	ErrFrameCapacity = errors.New("streaming encoded frame exceeds limit")
	// ErrObsolete acknowledges a canceled/replaced owner without delivery.
	ErrObsolete = errors.New("streaming delivery owner is obsolete")
	// ErrWriterClosed rejects delivery while closing or after writer termination.
	ErrWriterClosed = errors.New("streaming writer is closing")
	// ErrWriterRunning rejects reuse of the single connection writer loop.
	ErrWriterRunning = errors.New("streaming writer already consumed")
)

// WriterOptions bounds connection-retained jobs and immutable bytes. Application
// is a shared global retained-byte budget; nil selects connection-only accounting.
// Owners gates subscription jobs; nil permits only unowned connection messages.
// KeepAlive, when provided, runs serially in Run, never in a timer goroutine.
// Callbacks must honor context or transport write deadlines. Cancellation alone
// does not join callbacks or release their in-flight memory reservations.
type WriterOptions struct {
	MaxJobs                       int
	MaxFrameBytes, MaxQueuedBytes int64
	Application                   *Budget
	Owners                        *Subscriptions
	KeepAliveInterval             time.Duration
	KeepAlive                     func(context.Context) error
}

// WriteFrame synchronously writes and flushes one immutable, complete encoded
// frame. Returning nil acknowledges local transport delivery, not browser receipt.
type WriteFrame func(context.Context, []byte) error

type outboundJob struct {
	ctx             context.Context
	frame           []byte
	owner           *Operation
	terminal, fence bool
	ack             chan error
	local, global   *Reservation
}

// Writer owns one physical connection's application writes. NewWriter starts no
// work; the connection calls Run exactly once and owns its goroutine/join. Jobs
// are immutable copies and successful Deliver waits for a write acknowledgement.
// A legacy replacement must Fence before activating its successor. The old token
// has already been retired, so any later old jobs are rejected or discarded.
type Writer struct {
	ctx              context.Context
	cancel           context.CancelFunc
	options          WriterOptions
	budget           *Budget
	write            WriteFrame
	mu               sync.Mutex
	jobs             []*outboundJob
	outstanding      int
	started, closing bool
	failure          error
	wake             chan struct{}
	done             chan struct{}
}

// NewWriter validates copied limits. Zero limits select 64 jobs, a 16 MiB frame,
// and 32 MiB retained connection bytes (including its in-flight write).
func NewWriter(ctx context.Context, options WriterOptions, write WriteFrame) (*Writer, error) {
	if ctx == nil || write == nil || options.MaxJobs < 0 || options.MaxFrameBytes < 0 || options.MaxQueuedBytes < 0 || options.KeepAliveInterval < 0 || (options.KeepAlive == nil) != (options.KeepAliveInterval == 0) {
		return nil, ErrControl
	}
	if options.MaxJobs == 0 {
		options.MaxJobs = 64
	}
	if options.MaxFrameBytes == 0 {
		options.MaxFrameBytes = 16 << 20
	}
	if options.MaxQueuedBytes == 0 {
		options.MaxQueuedBytes = 32 << 20
	}
	budget, err := NewBudget(options.MaxQueuedBytes)
	if err != nil {
		return nil, err
	}
	work, cancel := context.WithCancel(ctx)
	return &Writer{ctx: work, cancel: cancel, options: options, budget: budget, write: write, wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Done closes only when Run has joined its active callback and discarded queued
// bytes. Close before Run closes it immediately without invoking any callbacks.
func (w *Writer) Done() <-chan struct{} { return w.done }

// RetainedBytes includes queued/in-flight frames and caller-owned baselines until
// their respective acknowledgement or explicit release.
func (w *Writer) RetainedBytes() int64 { return w.budget.Used() }

// ReserveBaseline accounts retained collection snapshots in the same connection
// and application budgets as queued/in-flight frames. Callers discard the value
// before releasing. Exhaustion closes the slow connection, never evicting state.
// The returned release is idempotent and remains valid after Writer closes.
func (w *Writer) ReserveBaseline(bytes int64) (func(), error) {
	w.mu.Lock()
	if w.closing {
		w.mu.Unlock()
		return nil, ErrWriterClosed
	}
	local, err := w.budget.Acquire(bytes)
	var global *Reservation
	if err == nil && w.options.Application != nil {
		global, err = w.options.Application.Acquire(bytes)
	}
	w.mu.Unlock()
	if err != nil {
		local.Release()
		global.Release()
		w.stop(err)
		return nil, err
	}
	return func() { local.Release(); global.Release() }, nil
}

// Deliver admits without blocking on a full queue and waits for acknowledged
// delivery. Capacity rejection closes the writer; oversized frames affect only
// the caller's subscription. Owners are rechecked at the actual write boundary.
func (w *Writer) Deliver(ctx context.Context, frame []byte, owner *Operation) error {
	return w.deliver(ctx, frame, owner, false, false)
}

// DeliverTerminal writes a final frame and retires that owner before acknowledging
// it. No following frame for that token can pass the writer ownership gate.
func (w *Writer) DeliverTerminal(ctx context.Context, frame []byte, owner *Operation) error {
	if owner == nil {
		return ErrControl
	}
	return w.deliver(ctx, frame, owner, true, false)
}

// Fence waits until earlier writes are completed or discarded. It performs no
// network write and is essential before activating a legacy replacement source.
func (w *Writer) Fence(ctx context.Context) error { return w.deliver(ctx, nil, nil, false, true) }

func (w *Writer) owns(owner *Operation) bool {
	return owner == nil || w.options.Owners != nil && w.options.Owners.Owns(owner)
}

func (w *Writer) deliver(ctx context.Context, frame []byte, owner *Operation, terminal, fence bool) error {
	if ctx == nil {
		return ErrControl
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !w.owns(owner) {
		return ErrObsolete
	}
	if int64(len(frame)) > w.options.MaxFrameBytes {
		return ErrFrameCapacity
	}
	job := &outboundJob{ctx: ctx, owner: owner, terminal: terminal, fence: fence, ack: make(chan error, 1)}
	w.mu.Lock()
	if w.closing || w.ctx.Err() != nil {
		w.mu.Unlock()
		return ErrWriterClosed
	}
	if w.outstanding >= w.options.MaxJobs {
		w.mu.Unlock()
		w.stop(ErrWriterCapacity)
		return ErrWriterCapacity
	}
	var err error
	job.local, err = w.budget.Acquire(int64(len(frame)))
	if err == nil && w.options.Application != nil {
		job.global, err = w.options.Application.Acquire(int64(len(frame)))
	}
	if err != nil {
		job.local.Release()
		w.mu.Unlock()
		w.stop(err)
		return err
	}
	// Copy only after both byte budgets and job admission have succeeded.
	job.frame = slices.Clone(frame)
	w.jobs = append(w.jobs, job)
	w.outstanding++
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	// Successful terminal retirement itself cancels the owner. Its waiter must
	// observe the actual acknowledgement, not mistake that cancellation for a
	// failed terminal write. Pre-write cancellation is still rejected in Run.
	var canceled <-chan struct{}
	if !terminal {
		canceled = ctx.Done()
	}
	select {
	case err := <-job.ack:
		return err
	case <-canceled:
		return ctx.Err()
	case <-w.ctx.Done():
		// Cancellation cannot assert write success. Run still owns reservations
		// until the actual callback has joined, even when callers stop waiting.
		select {
		case err := <-job.ack:
			return err
		default:
			return ErrWriterClosed
		}
	}
}

func (w *Writer) stop(err error) {
	w.mu.Lock()
	w.closing = true
	if w.failure == nil {
		w.failure = err
	}
	if !w.started {
		w.started = true
		w.discardLocked(ErrWriterClosed)
		close(w.done)
	}
	w.mu.Unlock()
	w.cancel()
}

func (w *Writer) acknowledgeLocked(job *outboundJob, err error) {
	job.frame = nil
	job.local.Release()
	job.global.Release()
	w.outstanding--
	job.ack <- err
}
func (w *Writer) discardLocked(err error) {
	for _, job := range w.jobs {
		w.acknowledgeLocked(job, err)
	}
	w.jobs = nil
}

// Run is the sole application writer; callbacks are never invoked under state
// mutexes. A stalled callback leaves Done open and bytes retained until it joins.
// Errors cancel all queued delivery; there is no retry of ambiguous writes.
func (w *Writer) Run() error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return ErrWriterRunning
	}
	w.started = true
	w.mu.Unlock()
	defer func() {
		w.cancel()
		w.mu.Lock()
		w.closing = true
		w.discardLocked(ErrWriterClosed)
		close(w.done)
		w.mu.Unlock()
	}()
	var timer *time.Ticker
	var tick <-chan time.Time
	if w.options.KeepAlive != nil {
		timer = time.NewTicker(w.options.KeepAliveInterval)
		tick = timer.C
		defer timer.Stop()
	}
	for {
		if w.ctx.Err() != nil {
			return w.outcome()
		}
		w.mu.Lock()
		var job *outboundJob
		if len(w.jobs) > 0 {
			job = w.jobs[0]
			w.jobs[0] = nil
			w.jobs = w.jobs[1:]
		}
		w.mu.Unlock()
		if job != nil {
			var err error
			attempted := false
			if job.ctx.Err() != nil {
				err = job.ctx.Err()
			} else if !w.owns(job.owner) {
				err = ErrObsolete
			} else if !job.fence {
				// Once started, a write belongs to the physical connection. A replaced
				// subscription cannot retract its bytes or cancel it halfway through.
				// The callback must use bounded transport writes; legacy successors
				// wait behind a fence while revision-aware clients discard old frames.
				attempted = true
				err = boundary.Call(w.ctx, func(ctx context.Context) error { return w.write(ctx, job.frame) })
				if err == nil && job.terminal {
					w.options.Owners.Terminate(job.owner)
				}
			}
			w.mu.Lock()
			w.acknowledgeLocked(job, err)
			w.mu.Unlock()
			if err != nil && attempted {
				w.stop(err)
				return err
			}
			continue
		}
		select {
		case <-w.ctx.Done():
			return w.outcome()
		case <-w.wake:
		case <-tick:
			if err := boundary.Call(w.ctx, w.options.KeepAlive); err != nil {
				w.stop(err)
				return err
			}
		}
	}
}

func (w *Writer) outcome() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return w.failure
	}
	return w.ctx.Err()
}

// Close cancels and explicitly joins the writer. A timeout leaves in-flight
// work/reservations owned; later Close continues joining. Normal cancellation is
// a clean close, while transport/overload failure remains inspectable.
func (w *Writer) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrControl
	}
	w.stop(nil)
	select {
	case <-w.done:
		err := w.outcome()
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
