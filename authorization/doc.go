// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package authorization compiles explicit declarations and evaluates roles before
// constructing policies. Prepared captures security metadata, not a permission:
// operations must evaluate it and recheck it before invoking a handler. Tenant
// membership remains an independent boundary, including on public operations.
package authorization
