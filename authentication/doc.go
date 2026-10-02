// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package authentication runs ordered trusted authentication handlers. It never
// trusts cookies or forwarded headers automatically and writes no HTTP response.
// Hosting must terminate failed credentials (401); exhaustion remains anonymous
// for authorization to decide. Anonymous role denial is ordinarily 403.
package authentication
