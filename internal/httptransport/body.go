// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package httptransport implements bounded unary HTTP input and encoding.
package httptransport

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// ReadBody enforces media/encoding policy and returns an HTTP override on failure.
func ReadBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, int, error) {
	var contentType, encoding []string
	for name, values := range r.Header {
		if strings.EqualFold(name, "Content-Type") {
			contentType = append(contentType, values...)
		}
		if strings.EqualFold(name, "Content-Encoding") {
			encoding = append(encoding, values...)
		}
	}
	unsupported := len(contentType) > 1 || len(encoding) > 1
	if len(encoding) == 1 && encoding[0] != "" && !strings.EqualFold(encoding[0], "identity") {
		unsupported = true
	}
	if len(contentType) == 1 && contentType[0] != "" {
		media, parameters, err := mime.ParseMediaType(contentType[0])
		if err != nil || media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json")) || parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8") {
			unsupported = true
		}
	}
	if unsupported {
		return nil, 415, errors.New("unsupported request representation")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return nil, 413, err
		}
		return nil, 400, err
	}
	return body, 0, nil
}
