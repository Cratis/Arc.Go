// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import type { ChangeSet, QueryResultWithState, Sorting } from '@cratis/arc/queries';
import type { SetSorting, SetPage, SetPageSize, ObservableQueryWhen } from '@cratis/arc.react/queries';
import { Watch, Task, Single, Nullable, Alias, Page, Changes, type WatchParameters } from './Generated/Shop/Tasks';

// Compile-only assignments; no mounted hook or browser behavior is asserted.
const single: () => [QueryResultWithState<Task>] = Single.use;
const suspenseSingle: () => [QueryResultWithState<Task>] = Single.useSuspense;
const nullable: () => [QueryResultWithState<Task | null>] = Nullable.use;
const collection: (args?: WatchParameters, sorting?: Sorting) => [QueryResultWithState<Task[]>, SetSorting] = Watch.use;
const suspense: typeof collection = Watch.useSuspense;
const paged: (size: number, args?: WatchParameters, sorting?: Sorting) => [QueryResultWithState<Task[]>, SetSorting, SetPage, SetPageSize] = Watch.useWithPaging;
const suspensePaged: typeof paged = Watch.useSuspenseWithPaging;
const changes: (args?: WatchParameters, key?: (item: Task) => unknown, sorting?: Sorting) => ChangeSet<Task> = Watch.useChangeStream;
const noArguments: (sorting?: Sorting) => [QueryResultWithState<Task[]>, SetSorting] = Alias.use;
const noArgumentsPaged: (size: number, sorting?: Sorting) => [QueryResultWithState<Task[]>, SetSorting, SetPage, SetPageSize] = Page.useWithPaging;
const noArgumentsChanges: (key?: (item: Task) => unknown, sorting?: Sorting) => ChangeSet<Task> = Changes.useChangeStream;
const when: (condition: boolean) => ObservableQueryWhen<Watch, Task[], WatchParameters> = Watch.when;
const singleWhen: (condition: boolean) => ObservableQueryWhen<Single, Task> = Single.when;
void [single, suspenseSingle, nullable, collection, suspense, paged, suspensePaged, changes, noArguments, noArgumentsPaged, noArgumentsChanges, when, singleWhen];
