// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package declarationdomain

//arc:readmodel
type Model struct{ Name string }

//arc:command
type Command struct{ Name string }

func (Command) Handle() error { return nil }
