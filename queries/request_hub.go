// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

// ReadHub constructs a request from hub text/null arguments and already parsed
// flat controls. It preserves GET-equivalent omission/conversion while keeping
// explicit null membership inspectable. Input membership and text are copied.
func ReadHub(arguments map[string]*string, parameters Parameters) (Request, error) {
	entries := make(map[string]any, len(arguments))
	for name, value := range arguments {
		if value == nil {
			entries[name] = nil
		} else {
			entries[name] = *value
		}
	}
	a, err := NewArguments(entries)
	if err != nil {
		return Request{}, err
	}
	a.source = getInput
	return NewRequest(a, parameters), nil
}
