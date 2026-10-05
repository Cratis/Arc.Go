// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { registerHooks } from 'node:module';
import test from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { QueryHttpMethod } from '@cratis/arc/queries';

const locked = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url)));
for (const [name, version] of [['@cratis/arc', '22.48.2'], ['@cratis/arc.react', '22.48.2'], ['@cratis/fundamentals', '7.22.0'], ['react', '18.3.1']]) {
    assert.equal(locked.packages[`node_modules/${name}`].version, version);
    assert.equal(JSON.parse(readFileSync(new URL(`../node_modules/${name}/package.json`, import.meta.url))).version, version);
}
const modules = new Map(['@cratis/fundamentals', '@cratis/arc/queries', '@cratis/arc/reflection', '@cratis/arc.react/queries']
    .map(specifier => [specifier, import.meta.resolve(specifier)]));
registerHooks({ resolve(specifier, context, nextResolve) {
    if (modules.has(specifier)) return nextResolve(modules.get(specifier), context);
    if (specifier.startsWith('.') && context.parentURL?.includes('/Generated/')) {
        const candidate = new URL(specifier + '.js', context.parentURL);
        if (existsSync(fileURLToPath(candidate))) return nextResolve(candidate.href, context);
    }
    return nextResolve(specifier, context);
} });
assert.ok(process.env.ARC_QUERY_VALIDATION_COMPILED, 'set ARC_QUERY_VALIDATION_COMPILED to the tsc output directory');
const root = pathToFileURL(process.env.ARC_QUERY_VALIDATION_COMPILED + '/');
const { Search, SearchValidator } = await import(new URL('Generated/Shop/Validation/Search.js', root));
const cases = JSON.parse(readFileSync(new URL('cases.json', import.meta.url)));
assert.equal(cases.length, 22, 'complete shared server/client corpus');
const findings = values => values.map(({ severity, message, members }) => ({ severity, message, members }));

// Transport replies only support successful client paths here. Independent
// production-generated Go HTTP tests prove server rejection, not these mocks.
function withFetch(t) {
    const original = globalThis.fetch;
    const calls = [];
    globalThis.fetch = async (url, options) => {
        calls.push({ url: new URL(url), options });
        return Response.json({
            isSuccess: true, isReady: true, isAuthorized: true, isValid: true, hasExceptions: false,
            validationResults: [], exceptionMessages: [], exceptionStackTrace: '',
            paging: { page: 0, size: 0, totalItems: 0, totalPages: 0 }, data: []
        });
    };
    t.after(() => { globalThis.fetch = original; });
    return calls;
}

test('production query keeps exact wire identity, optionality, validator and descriptors', () => {
    const query = new Search();
    assert.equal(query.route, '/api/shop/validation/search');
    assert.equal(query.queryName, 'Shop.Validation.Match.Search');
    assert.ok(query.validation instanceof SearchValidator);
    assert.deepEqual(query.requiredRequestParameters, ['required']);
    assert.deepEqual(query.parameterDescriptors.map(value => value.name), ['EXACT-name', 'present', 'range', 'warning', 'info', 'required']);
    for (const parameter of query.parameterDescriptors) assert.equal(query[parameter.name], undefined);
});

for (const tc of cases) {
    test(`before transport: ${tc.name}`, async t => {
        const calls = withFetch(t);
        const input = Object.freeze(structuredClone(tc.input));
        const original = structuredClone(input);
        const validator = new SearchValidator();
        assert.deepEqual(findings(validator.validate(input)), tc.findings);
        for (const method of [QueryHttpMethod.Get, QueryHttpMethod.Query]) {
            for (const mode of ['args', 'parameters']) {
                const query = new Search();
                query.setOrigin('https://query-validation.invalid');
                query.setHttpMethod(method);
                if (mode === 'parameters') query.parameters = input;
                const before = calls.length;
                const result = mode === 'args' ? await query.perform(input) : await query.perform();
                const valid = tc.findings.length === 0;
                assert.equal(result.isSuccess, valid);
                assert.equal(result.isValid, valid);
                assert.deepEqual(findings(result.validationResults), tc.findings);
                assert.equal(calls.length - before, valid ? 1 : 0, 'validation must finish before fetch');
                if (valid) assert.equal(calls.at(-1).options.method, method === QueryHttpMethod.Get ? 'GET' : 'QUERY');
            }
        }
        assert.deepEqual(input, original, 'normalizing builtin empty-as-missing must not mutate arguments');
    });
}

test('pinned runtime validates args, not descriptor-backed instance overrides', async t => {
    const calls = withFetch(t);
    const query = new Search();
    query.setOrigin('https://query-validation.invalid');
    query.setHttpMethod(QueryHttpMethod.Get);
    query['EXACT-name'] = 'a';
    await query.perform({ 'EXACT-name': 'valid args', present: 'yes', required: 'yes' });
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url.searchParams.get('EXACT-name'), 'a');
    // This known upstream limit is not a client-side security boundary. The
    // independent generated server rejects the same invalid value regardless.
});
