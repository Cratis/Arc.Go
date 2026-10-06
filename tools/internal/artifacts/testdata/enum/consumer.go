// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

//arc:namespace EnumContract
package consumer

import "example.test/consumer/access"

// State keeps original parse names separate from TypeScript exports.
//
//arc:enum parse=int32 members=Read:Reader
type State int32

const (
	Zero     State = 0
	Read     State = 1
	Write    State = 4
	Alias    State = 4
	Negative State = -2
	High     State = 1 << 30
	Sign     State = -1 << 31
)

// Envelope exposes the enum through a real query contract.
//
//arc:readmodel
type Envelope struct {
	State  State
	Access access.Access
}

func (Envelope) All() ([]Envelope, error) { return nil, nil }
