// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
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
func jsonMembers(decoder *json.Decoder, opening json.Delim, depth int) error {
	if depth > 32 {
		return ErrControl
	}
	seen := map[string]bool{}
	for decoder.More() {
		if opening == '{' {
			token, err := decoder.Token()
			name, ok := token.(string)
			if err != nil || !ok || seen[strings.ToLower(name)] {
				return ErrControl
			}
			seen[strings.ToLower(name)] = true
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
