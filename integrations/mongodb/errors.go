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
