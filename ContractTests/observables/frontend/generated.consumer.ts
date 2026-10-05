// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { All, Item, Private } from './Generated/Contracts/Items';
import type { ChangeSet, QueryResultWithState } from '@cratis/arc/queries';
import type { SetPage, SetPageSize, SetSorting } from '@cratis/arc.react/queries';

// Signature evidence only: these hooks are NOT mounted in the Node runtime case.
export function signatures() {
    const query = new All();
    void query.perform({ group: 'alpha' });
    const subscription = query.subscribe(result => { const data: Item[] = result.data; void data; }, { group: 'alpha' });
    subscription.unsubscribe(); query.dispose();
    const normal: [QueryResultWithState<Item[]>, SetSorting] = All.use({ group: 'alpha' });
    const suspense: [QueryResultWithState<Item[]>, SetSorting] = All.useSuspense({ group: 'alpha' });
    const paged: [QueryResultWithState<Item[]>, SetSorting, SetPage, SetPageSize] = All.useWithPaging(10, { group: 'alpha' });
    const suspensePaged: typeof paged = All.useSuspenseWithPaging(10, { group: 'alpha' });
    const changes: ChangeSet<Item> = All.useChangeStream({ group: 'alpha' }, item => item.id);
    void [normal, suspense, paged, suspensePaged, changes, Private.when(true)];
}
