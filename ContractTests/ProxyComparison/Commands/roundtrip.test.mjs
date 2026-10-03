// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { registerHooks } from 'node:module';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { Guid } from '@cratis/fundamentals';

const lockedModules = new Map(['@cratis/fundamentals', '@cratis/arc/commands', '@cratis/arc/reflection', '@cratis/arc.react/commands']
    .map(specifier => [specifier, import.meta.resolve(specifier)]));
registerHooks({ resolve(specifier, context, nextResolve) {
    if (lockedModules.has(specifier)) return nextResolve(lockedModules.get(specifier), context);
    if (specifier.startsWith('.') && context.parentURL?.includes('/tsc-standard/')) {
        const candidate = new URL(specifier + '.js', context.parentURL);
        if (existsSync(fileURLToPath(candidate))) return nextResolve(candidate.href, context);
    }
    return nextResolve(specifier, context);
} });
const { CreateTask, Created } = await import('../../../.ai-work/output/ts-command/tsc-standard/Generated/Shop/Commands/index.js');

test('actual Arc taskboard host: one generated command validate/execute round-trip, no handwritten server envelopes', { timeout: 15000 }, async () => {
    const child = spawn(fileURLToPath(new URL('../../../.ai-work/output/ts-command/taskboard-host', import.meta.url)), [], { stdio: ['ignore', 'pipe', 'pipe'] });
    let stderr = '';
    child.stderr.on('data', data => { stderr = (stderr + data).slice(-4096); });
    const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
    const originalFetch = globalThis.fetch;
    try {
        const baseUrl = await new Promise((resolve, reject) => {
            let output = '';
            const timer = setTimeout(() => reject(new Error('fixture readiness timed out: ' + stderr)), 5000);
            child.once('error', error => { clearTimeout(timer); reject(error); });
            child.once('exit', code => { clearTimeout(timer); reject(new Error(`fixture exited before readiness ${code}: ${stderr}`)); });
            child.stdout.on('data', data => {
                output += data;
                if (output.length > 4096) { clearTimeout(timer); reject(new Error('oversized readiness')); return; }
                if (!output.includes('\n')) return;
                clearTimeout(timer);
                try {
                    const ready = JSON.parse(output.split('\n')[0]);
                    assert.equal(ready.kind, 'arc-go-conformance-ready');
                    assert.match(ready.baseUrl, /^http:\/\/127\.0\.0\.1:\d+$/);
                    resolve(ready.baseUrl);
                } catch (error) { reject(error); }
            });
        });
        const envelopes = [];
        globalThis.fetch = async (url, options) => {
            const response = await originalFetch(url, { ...options, signal: AbortSignal.timeout(3000) });
            assert.equal(response.status, 200);
            assert.equal(response.headers.get('X-Correlation-ID'), '00112233-4455-4677-8899-aabbccddeeff');
            envelopes.push(await response.clone().json());
            return response;
        };
        const command = new CreateTask();
        command.setOrigin(baseUrl);
        command.setHttpHeadersCallback(() => ({ 'X-Correlation-ID': '00112233-4455-4677-8899-aabbccddeeff' }));
        command.title = 'generated real round-trip';
        const validation = await command.validate();
        const expected = {
            correlationId: '00112233-4455-4677-8899-aabbccddeeff', isSuccess: true, isAuthorized: true, isValid: true,
            hasExceptions: false, validationResults: [], exceptionMessages: [], exceptionStackTrace: '', authorizationFailureReason: ''
        };
        assert.deepEqual(envelopes[0], expected); // Complete real envelope, including response absence.
        assert.equal(validation.isSuccess, true);
        assert.equal(validation.response, undefined);
        assert.equal(command.hasChanges, true);
        const execution = await command.execute();
        assert.ok(execution.response instanceof Created);
        assert.ok(execution.response.id instanceof Guid);
        const id = execution.response.id.toString();
        assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
        assert.notEqual(id, Guid.empty.toString());
        assert.equal(execution.response.title, 'generated real round-trip');
        assert.deepEqual(envelopes[1], { ...expected, response: { id, title: 'generated real round-trip' } });
        assert.equal(execution.correlationId.toString(), expected.correlationId);
        assert.equal(execution.isSuccess, true);
        assert.equal(command.hasChanges, false);
        assert.equal(envelopes.length, 2);
    } finally {
        globalThis.fetch = originalFetch;
        child.kill('SIGTERM');
        const timer = setTimeout(() => child.kill('SIGKILL'), 3000);
        try { const exit = await exited; assert.equal(exit.code, 0, stderr); }
        finally { clearTimeout(timer); }
    }
});
