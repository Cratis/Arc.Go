// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { field } from '@cratis/fundamentals';

// Hand-authored fixture of the admitted proxy shape, NOT arc-gen output.
// Original CLR parse names remain separate in normalized.json.
export enum State { zero = 0, reader = 1, writer = 4, alias = 4, high = 1073741824, sign = -2147483648, negative = -2 }
export enum Access { none = 0, reader = 1, writer = 4, alias = 4, high = 1073741824, sign = -2147483648 }
export const allAccess = Access.none | Access.reader | Access.writer | Access.alias | Access.high | Access.sign;

export class Model {
    @field(Number)
    state!: State;

    @field(Number)
    access!: Access;

    @field(Number, true)
    states!: State[];

    @field(Number)
    optional?: State;
}
