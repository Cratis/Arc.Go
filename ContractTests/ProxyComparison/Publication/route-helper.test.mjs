// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import test from 'node:test';
import { QueryFor, QueryHttpMethod } from '@cratis/arc/queries';

// This deliberately hand-authored invalid proxy characterizes the pinned runtime;
// the Go analyzer/renderer regression rejects generating its parameter contract.
class InvalidParameter extends QueryFor {
    route = '/api/listing/find';
    queryName = 'Shop.Listing.Find';
    defaultValue = {};
    parameterDescriptors = [];
    get requiredRequestParameters() { return []; }
    constructor() { super(Object, false); }
}
test('Arc 22.48.2 route helper rejects a[ before fetch for GET and QUERY', async () => {
    let requests = 0;
    const original = globalThis.fetch;
    globalThis.fetch = async () => { requests++; throw new Error('unexpected fetch'); };
    try {
        for (const method of [QueryHttpMethod.Get, QueryHttpMethod.Query]) {
            const query = new InvalidParameter();
            query.setOrigin('http://localhost:1234');
            query.setHttpMethod(method);
            const result = await query.perform({ 'a[': 'value' });
            assert.equal(result.isSuccess, false);
            assert.equal(result.hasExceptions, true);
            assert.match(result.exceptionMessages.join(' '), /regular expression|unterminated character class/i);
        }
        assert.equal(requests, 0);
    } finally { globalThis.fetch = original; }
});
