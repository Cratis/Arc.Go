// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
// Hand-authored import regression using the exact reserved binding asserted by
// TestTypeScriptImportsReserveGlobalSymbol. This is not generated-family evidence.
import { Symbol as Symbol_2 } from './Symbol';
import { field } from '@cratis/fundamentals';
export class Reference {
    @field(Symbol_2)
    value!: Symbol_2;
}
