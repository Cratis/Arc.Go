// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { All, Array as ArrayQuery, Find, GetOnly, Paged, QueryOnly, Tasks, ByID } from './Generated/Shop/Queries/index';
import type { Listing, FindParameters, Task } from './Generated/Shop/Queries/index';
import type { QueryResultWithState } from '@cratis/arc/queries';
import type { PerformQuery, SetSorting, SetPage, SetPageSize } from '@cratis/arc.react/queries';
import { Guid } from '@cratis/fundamentals';

const args: FindParameters = { id: Guid.empty, count: 0, enabled: false, tags: [] };
const single: [QueryResultWithState<Listing>, PerformQuery<FindParameters>, SetSorting] = Find.use(args);
const suspense: typeof single = Find.useSuspense(args);
const list: [QueryResultWithState<Listing[]>, PerformQuery, SetSorting] = All.use(All.sortBy.name.ascending);
const paged: [QueryResultWithState<Listing[]>, PerformQuery, SetSorting, SetPage, SetPageSize] = Paged.useWithPaging(10, args, Paged.sortBy.status.descending);
const pagedSuspense: typeof paged = Paged.useSuspenseWithPaging(5, args);
const ordinaryListPaging: typeof paged = All.useWithPaging(10);
const arrayTuple: typeof list = ArrayQuery.useSuspense();
const getTuple: typeof list = GetOnly.use();
const queryTuple: typeof list = QueryOnly.use();
const taskTuple: [QueryResultWithState<Task[]>, PerformQuery, SetSorting] = Tasks.use();
const taskSingle: [QueryResultWithState<Task>, PerformQuery<{id: Guid}>, SetSorting] = ByID.use({id: Guid.empty});
All.when(false); Find.when(true); Paged.when(true);
new Paged().sortBy.name.ascending();
// @ts-expect-error Required id cannot be dropped from the caller argument interface.
const missing: FindParameters = { enabled: false };
// @ts-expect-error Wrong argument scalar.
Find.use({ id: 'not-a-Guid' });
// @ts-expect-error No-arguments hooks accept sorting, not an arbitrary args object.
All.use({ id: Guid.empty });
// @ts-expect-error Three-element tuples are not paged tuples.
const wrongTuple: typeof paged = All.use();
// @ts-expect-error Snapshot list data is an array, not a Page/provider wrapper.
const wrongData: QueryResultWithState<{items: Listing[]}> = list[0];
// @ts-expect-error Sorting is declared result-wire fields, not query arguments.
Paged.sortBy.id;
// @ts-expect-error Complex fields are not sortable.
All.sortBy.detail;
// @ts-expect-error Single snapshots do not acquire paging hooks.
Find.useWithPaging(10, args);
void [single,suspense,list,paged,pagedSuspense,ordinaryListPaging,arrayTuple,getTuple,queryTuple,taskTuple,taskSingle,missing,wrongTuple,wrongData];
