// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { All, Observe, Register, Status } from './Snapshots/Primary22.48.2/ProxyComparison';
import { Guid } from '@cratis/fundamentals';

// Compile-only characterization; hooks run only under React in the later paired suite.
export function characterize() {
    const command = new Register();
    command.id = Guid.parse('12345678-90ab-cdef-0123-456789abcdef');
    command.name = 'name';
    command.quantity = 1;
    const [snapshot, perform, setSorting, setPage, setPageSize] = All.useWithPaging(2, { id: command.id });
    const [observable, setObservableSorting, setObservablePage, setObservablePageSize] = Observe.useWithPaging(2, { id: command.id });
    const _enum: Status = Status.draft;
    // The upstream defect is captured, not corrected in the reference: argument-based sorting.
    const _sorting = All.sortBy.id.ascending;
    // @ts-expect-error Required Guid query argument cannot be a string.
    All.use({ id: 'not-a-guid' });
    // @ts-expect-error Snapshot paging has exactly five members.
    const [, , , , , extra] = All.useWithPaging(2);
    return { snapshot, perform, setSorting, setPage, setPageSize, observable, setObservableSorting, setObservablePage, setObservablePageSize, _enum, _sorting, extra };
}
