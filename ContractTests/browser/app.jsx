// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// The browser application under test. It mounts the real exported <Arc> wrapper
// with production-generated hooks; there is no test reducer, transport shim or
// handwritten query class. Only the transport selection and StrictMode/retention
// come from the URL, so each case runs ordinary application code.
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Arc } from '@cratis/arc.react';
import { QueryTransportMethod } from '@cratis/arc/queries';
import { All } from '../observables/frontend/Generated/Contracts/Items';

const parameters = new URLSearchParams(window.location.search);
const group = parameters.get('group') ?? 'alpha';
const webSocket = parameters.get('transport') === 'ws';
const strict = parameters.get('strict') === '1';
const retention = parameters.has('retention') ? Number(parameters.get('retention')) : undefined;

// Each committed render appends one projection of the hook-returned result. The
// runner asserts on these projections and the DOM; it never reads Arc internals.
window.__arcRenders = [];

function Items() {
    const [result] = All.use({ group });
    const items = result.data ?? [];
    const state = {
        isReady: result.isReady,
        isSuccess: result.isSuccess,
        isAuthorized: result.isAuthorized,
        titles: items.map(item => item.title),
        hydrated: items.every(item => item.createdAt instanceof Date && !Number.isNaN(item.createdAt.getTime())),
    };
    window.__arcRenders.push(state);
    window.__arcState = state;
    return (
        <section data-ready={String(result.isReady)} data-authorized={String(result.isAuthorized)}>
            <ul id="items">{items.map(item => <li key={item.id} data-id={item.id}>{item.title}</li>)}</ul>
        </section>
    );
}

// The default <Arc> case passes no transport prop, so it uses the wrapper's
// own default (the SSE hub), exactly like an application that relies on it.
const transport = webSocket ? { queryTransportMethod: QueryTransportMethod.WebSocket } : {};
const cache = retention === undefined ? {} : { queryCacheRetentionMs: retention };
const application = <Arc {...transport} {...cache}><Items /></Arc>;
const root = createRoot(document.getElementById('root'));
root.render(strict ? <StrictMode>{application}</StrictMode> : application);
window.__unmount = () => root.unmount();
