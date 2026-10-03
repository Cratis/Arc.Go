// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { field } from '@cratis/fundamentals';
import { ObservableQueryFor } from '@cratis/arc/queries';

// Hand-declared proxy: Go TypeScript generation is not part of this evidence.
export class Item {
    @field(String) id!: string;
    @field(String) title!: string;
}
export class Items extends ObservableQueryFor<Item[], { group: string }> {
    readonly route = '/items';
    readonly defaultValue: Item[] = [];
    readonly parameterDescriptors = [];
    get requiredRequestParameters() { return ['group']; }
    constructor(readonly queryName: string) { super(Item, true); }
}
export class NilItems extends ObservableQueryFor<Item[] | null, { group: string }> {
    readonly route = '/items';
    readonly defaultValue = null;
    readonly parameterDescriptors = [];
    get requiredRequestParameters() { return ['group']; }
    // Non-enumerable preserves absence instead of normalizing it to an array.
    constructor(readonly queryName: string) { super(Item, false); }
}
