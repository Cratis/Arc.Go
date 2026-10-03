// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

// Message is the hub wire envelope. Absent optional fields are omitted, not null.
type Message struct {
	Type                          string    `json:"type"`
	QueryID                       string    `json:"queryId,omitempty"`
	Revision                      *Revision `json:"revision,omitempty"`
	Payload                       any       `json:"payload,omitempty"`
	Timestamp                     *int64    `json:"timestamp,omitempty"`
	KeepAliveIntervalMs           int64     `json:"keepAliveIntervalMs,omitempty"`
	SupportsSubscriptionRevisions bool      `json:"supportsSubscriptionRevisions,omitempty"`
}

// SubscriptionRequest is distinct from the QUERY JSON body. Arguments are text
// or explicit null; paging and sorting controls are flat.
type SubscriptionRequest struct {
	QueryName     string             `json:"queryName"`
	Arguments     map[string]*string `json:"arguments"`
	Page          *int32             `json:"page"`
	PageSize      *int32             `json:"pageSize"`
	SortBy        *string            `json:"sortBy"`
	SortDirection *string            `json:"sortDirection"`
	TransferMode  *string            `json:"transferMode"`
}

// SSEControl is the SSE POST envelope; WebSocket uses payload, not request.
type SSEControl struct {
	ConnectionID string               `json:"connectionId"`
	QueryID      string               `json:"queryId"`
	Revision     *Revision            `json:"revision"`
	Request      *SubscriptionRequest `json:"request"`
}

// ParseSSEControl accepts exactly one JSON object. Duplicate case-insensitive
// keys and excessive nesting fail closed rather than overriding revision/owner
// fields. Unknown fields remain permissive like the reference JSON contract.
func ParseSSEControl(body []byte, subscribe bool) (SSEControl, error) {
	if err := uniqueJSON(body); err != nil {
		return SSEControl{}, err
	}
	var control SSEControl
	if subscribe {
		if err := json.Unmarshal(body, &control); err != nil {
			return SSEControl{}, ErrControl
		}
	} else {
		// request is not part of the unsubscribe DTO; treat it like any other
		// unknown field instead of applying subscribe-specific value constraints.
		var unsubscribe struct {
			ConnectionID string    `json:"connectionId"`
			QueryID      string    `json:"queryId"`
			Revision     *Revision `json:"revision"`
		}
		if err := json.Unmarshal(body, &unsubscribe); err != nil {
			return SSEControl{}, ErrControl
		}
		control.ConnectionID, control.QueryID, control.Revision = unsubscribe.ConnectionID, unsubscribe.QueryID, unsubscribe.Revision
	}
	if control.ConnectionID == "" || len(control.ConnectionID) > 256 || control.QueryID == "" || len(control.QueryID) > 256 {
		return SSEControl{}, ErrControl
	}
	if subscribe {
		request := control.Request
		if request == nil || request.QueryName == "" || len(request.QueryName) > 1024 || len(request.Arguments) > 128 {
			return SSEControl{}, ErrControl
		}
		for name := range request.Arguments {
			if name == "" {
				return SSEControl{}, ErrControl
			}
		}
	}
	return control, nil
}

// WSControl uses payload for subscribe, unlike the SSE request envelope.
// Absent Ping timestamps are filled by the transport clock.
type WSControl struct {
	Type      string
	QueryID   string
	Revision  *Revision
	Request   *SubscriptionRequest
	Timestamp *int64
}

// ParseWSControl accepts bounded, unique-member JSON controls. Socket identity
// is fixed by the handshake; client-supplied connection/identity fields are not
// used to authorize subscriptions.
func ParseWSControl(body []byte, hub bool) (WSControl, error) {
	if err := uniqueJSON(body); err != nil {
		return WSControl{}, err
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return WSControl{}, ErrControl
	}
	switch {
	case strings.EqualFold(envelope.Type, "Ping"), strings.EqualFold(envelope.Type, "Pong"):
		var ping struct {
			Timestamp *int64 `json:"timestamp"`
		}
		if err := json.Unmarshal(body, &ping); err != nil {
			return WSControl{}, ErrControl
		}
		kind := "Ping"
		if strings.EqualFold(envelope.Type, "Pong") {
			kind = "Pong"
		}
		return WSControl{Type: kind, Timestamp: ping.Timestamp}, nil
	case hub && strings.EqualFold(envelope.Type, "Subscribe"):
		var subscribe struct {
			QueryID  string               `json:"queryId"`
			Revision *Revision            `json:"revision"`
			Payload  *SubscriptionRequest `json:"payload"`
		}
		if err := json.Unmarshal(body, &subscribe); err != nil {
			return WSControl{}, ErrControl
		}
		request := subscribe.Payload
		if subscribe.QueryID == "" || len(subscribe.QueryID) > 256 || request == nil || request.QueryName == "" || len(request.QueryName) > 1024 || len(request.Arguments) > 128 {
			return WSControl{}, ErrControl
		}
		for name := range request.Arguments {
			if name == "" {
				return WSControl{}, ErrControl
			}
		}
		return WSControl{Type: "Subscribe", QueryID: subscribe.QueryID, Revision: subscribe.Revision, Request: request}, nil
	case hub && strings.EqualFold(envelope.Type, "Unsubscribe"):
		var unsubscribe struct {
			QueryID  string    `json:"queryId"`
			Revision *Revision `json:"revision"`
		}
		if err := json.Unmarshal(body, &unsubscribe); err != nil || unsubscribe.QueryID == "" || len(unsubscribe.QueryID) > 256 {
			return WSControl{}, ErrControl
		}
		return WSControl{Type: "Unsubscribe", QueryID: unsubscribe.QueryID, Revision: unsubscribe.Revision}, nil
	default:
		return WSControl{}, ErrControl
	}
}

func uniqueJSON(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return ErrControl
	}
	if err := jsonMembers(decoder, '{', 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrControl
	}
	return nil
}

// JSON's case-insensitive member matching uses Unicode simple folding, not
// lowercase alone (for example ASCII s and long s). Canonicalize the whole fold
// class so alternate spellings cannot override owner/revision fields.
func foldedJSONName(name string) string {
	return strings.Map(func(r rune) rune {
		least := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < least {
				least = next
			}
		}
		return least
	}, name)
}

func jsonMembers(decoder *json.Decoder, opening json.Delim, depth int) error {
	if depth > 32 {
		return ErrControl
	}
	seen := map[string]bool{}
	for decoder.More() {
		if opening == '{' {
			token, err := decoder.Token()
			name, ok := token.(string)
			if err != nil || !ok {
				return ErrControl
			}
			folded := foldedJSONName(name)
			if seen[folded] {
				return ErrControl
			}
			seen[folded] = true
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrControl
		}
		if delim, ok := token.(json.Delim); ok {
			if delim != '{' && delim != '[' {
				return ErrControl
			}
			if err := jsonMembers(decoder, delim, depth+1); err != nil {
				return err
			}
		}
	}
	end, err := decoder.Token()
	if err != nil || opening == '{' && end != json.Delim('}') || opening == '[' && end != json.Delim(']') {
		return ErrControl
	}
	return nil
}
