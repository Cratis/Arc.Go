// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package services provides exact-type explicit registrations, isolated operation scopes,
// and owned resource cleanup. It performs no constructor discovery or command completion.
// Registries are single-owner builders; providers and ordinary scopes support concurrent use.
// Resolved application services need their own concurrency guarantees.
package services
