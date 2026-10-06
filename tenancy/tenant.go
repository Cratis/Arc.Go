// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy

// Tenant is development display data, not membership evidence.
type Tenant struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}
