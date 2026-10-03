// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import "errors"

// ErrConfiguration identifies invalid binding configuration or coordinates.
var ErrConfiguration = errors.New("invalid MongoDB configuration")

// ErrUnsupportedModel identifies a model outside the declared storage profile.
var ErrUnsupportedModel = errors.New("unsupported MongoDB model")

// ErrValue identifies invalid BSON or a scalar outside its declared range.
// Codec failures do not include document contents or application codec messages.
var ErrValue = errors.New("invalid MongoDB value")

// ErrLimit identifies output, request or paging metadata beyond supported bounds.
// No truncated data or partial total is returned.
var ErrLimit = errors.New("MongoDB snapshot limit exceeded")

// ErrReleaseRequired identifies a Chronicle-owned renderer without mandatory
// application-provided Chronicle release. Bindings alone cannot publish sinks.
var ErrReleaseRequired = errors.New("MongoDB Chronicle release required")
