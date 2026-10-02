// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Validate the whole document's depth, including unknown fields and untyped values.
// Per-value recursion checks additionally bound pointers and optional wrappers.
func validateJSON(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("expected one complete JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
			if depth > 64 {
				return fmt.Errorf("JSON nesting exceeds 64 levels")
			}
		}
	}
}
