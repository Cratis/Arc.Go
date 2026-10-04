// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"net/http"
	"strings"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/internal/httptransport"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// ActionResult is a traditional action's output, not a model-bound command
// outcome. Response is borrowed until publication; nil omits the response.
// ValidationResults are safe client-visible findings added by the action.
// Raw explicitly opts a successful action out of envelope publication and owns
// status, headers, body, and write errors through ordinary net/http semantics.
// Raw receives the request after body binding and must not retain the writer.
// Errors or blocking findings suppress both
// Response and Raw. No response is interpreted as an event or operation.
//
// The action must return either Response or Raw, not both.
type ActionResult struct {
	Response          any
	ValidationResults []validation.Result
	Raw               http.Handler
}

// ActionOptions configures an explicit traditional POST adapter. Callbacks are
// borrowed, synchronous and may run concurrently for different requests. They
// must honor cancellation and must not mutate returned values during publication.
// There is no command registry, Provide, scope, transaction or event processing.
type ActionOptions[T any] struct {
	// FromRequest optionally binds query/route values into a fresh T. The caller
	// chooses their precedence and returns malformed input as *commands.DecodeError;
	// other errors are redacted. It must not read Body or perform business effects.
	// Nondefault request values fill default body fields, including body 0/false.
	// Explicit body empty strings and nonnil scalar pointers win. This bounded
	// profile admits flat built-in scalars and scalar pointers, not custom codecs,
	// collections or embedded fields. Wire names match exactly, as in Arc's JSON
	// serializer. Null plain strings are treated as missing during merging; use
	// *string to retain null separately from empty. Without FromRequest, only the
	// JSON body is bound using the serializer's normal null rules.
	FromRequest func(*http.Request) (T, error)
	// Validate optionally returns safe input findings before any action. It also
	// runs on /validate requests. Nil means no application validation.
	Validate func(context.Context, T) ([]validation.Result, error)
	// TreatWarningsAsErrors retains Warning as well as Error findings. By default
	// only Error findings block; X-Ignore-Warnings: true overrides this option.
	TreatWarningsAsErrors bool
	// MaxBodyBytes defaults to 1 MiB. Negative limits are invalid.
	MaxBodyBytes int64
	// MaxQueryBytes defaults to 8 KiB, measured before invoking FromRequest.
	MaxQueryBytes int
	// MaxResponseBytes defaults to 16 MiB for envelopes. Raw owns its own limits.
	MaxResponseBytes int64
}

// NewActionHandler adapts an explicit traditional POST action to Arc envelopes.
// Mount it with Builder.Handle for both the action and its /validate route; no
// routes are registered automatically. A case-insensitive /validate suffix runs
// binding and validation but NEVER the action or a Raw handler. Other methods
// receive 405. T must be a struct without methods; invalid configuration fails
// construction without invoking callbacks. An absent body binds a zero T; a
// present body must be one JSON object. Unknown properties are ignored and
// duplicate declared wire names rejected, as in the existing serializer.
//
// The adapter consumes hosting correlation/identity/tenant context, but does not
// invoke catalog authorization policies. Protect it with application middleware,
// like any Builder.Handle route. Used outside Arc, correlation is zero unless
// supplied through correlation.WithID. Errors/panics before raw publication
// use production redaction regardless of the host's development settings. Raw
// handlers are an explicit unwrapped net/http boundary, not buffered or recovered
// here after they may have committed headers.
func NewActionHandler[T any](action func(context.Context, T) (ActionResult, error), options ActionOptions[T]) (http.Handler, error) {
	if action == nil {
		return nil, ErrInvalidOptions
	}
	bind, err := newActionRequest[T](options.FromRequest != nil)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeOptions(Options{HTTP: HTTPOptions{
		MaxBodyBytes: options.MaxBodyBytes, MaxQueryBytes: options.MaxQueryBytes, MaxResponseBytes: options.MaxResponseBytes,
	}})
	if err != nil {
		return nil, err
	}
	// Only the existing bounded publication primitive is used. This value is
	// never built or activated and cannot run a command/query pipeline.
	publisher := &Application{options: normalized}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id := correlation.FromContext(r.Context())
		if len(r.URL.RawQuery) > normalized.HTTP.MaxQueryBytes {
			publisher.publish(w, r, http.StatusBadRequest, commands.InvalidBody(id))
			return
		}
		body, status, err := httptransport.ReadBody(w, r, normalized.HTTP.MaxBodyBytes)
		if err != nil {
			publisher.publish(w, r, status, commands.InvalidBody(id))
			return
		}
		var output ActionResult
		var findings []validation.Result
		err = boundary.Call(r.Context(), func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			var request T
			if options.FromRequest != nil {
				var err error
				request, err = options.FromRequest(r)
				if err != nil {
					return err
				}
			}
			input, err := bind(body, request)
			if err != nil {
				return &commands.DecodeError{Cause: err}
			}
			if options.Validate != nil {
				findings, err = options.Validate(ctx, input)
				if err != nil {
					return err
				}
			}
			findings = actionFindings(findings, options.TreatWarningsAsErrors, r)
			if len(findings) != 0 || strings.HasSuffix(strings.ToLower(r.URL.Path), "/validate") {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			output, err = action(ctx, input)
			findings = append(findings, actionFindings(output.ValidationResults, options.TreatWarningsAsErrors, r)...)
			if err == nil && output.Raw != nil && !nilValue(output.Response) {
				return ErrInvalidOptions
			}
			return err
		})
		result := commands.NewResult(commands.Details{CorrelationID: id, Authorized: true, ValidationResults: findings}, serialization.Some(output.Response))
		if err != nil {
			failure := commands.FromError[commands.NoResponse](id, err)
			// An error with no classified findings is still a failure. Never
			// publish its response or invoke Raw merely because classification
			// produced a successful-looking fragment.
			if failure.IsSuccess() {
				failure = commands.NewResult(commands.Details{
					CorrelationID: id, Authorized: true,
					ExceptionMessages: []string{boundary.InternalErrorMessage},
				}, serialization.Optional[commands.NoResponse]{})
			}
			result = commands.Merge(result, failure)
		}
		if result.IsSuccess() && !nilValue(output.Raw) {
			output.Raw.ServeHTTP(w, r)
			return
		}
		publisher.publish(w, r, result.StatusCode(), result)
	}), nil
}

func actionFindings(findings []validation.Result, warnings bool, r *http.Request) []validation.Result {
	ignore := headerValues(r.Header, "X-Ignore-Warnings")
	if len(ignore) == 1 && strings.EqualFold(strings.TrimSpace(ignore[0]), "true") {
		warnings = false
	}
	var retained []validation.Result
	for _, finding := range findings {
		if finding.Severity == validation.Error || warnings && finding.Severity >= validation.Warning {
			retained = append(retained, finding.Clone())
		}
	}
	return retained
}
