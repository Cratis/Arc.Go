// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { access, mkdir, mkdtemp, readFile, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { performance } from 'node:perf_hooks';
import test from 'node:test';
import ts from 'typescript';
import { compare, compareFragments, fragments, pairedFiles } from './compare.mjs';
import { generate, prepare, tidyConsumer } from './Matched/prepare.mjs';
import { directory, run } from './helpers.mjs';

const toolsSum = join(directory, '../../tools/go.sum');
const toolsChecksums = await readFile(toolsSum, 'utf8');
const output = await generate();
const ledger = JSON.parse(await readFile(join(directory, 'Matched/allowances.json')));

test('production arc-gen output compares file-by-file against untouched pinned C# captures', async () => {
    await compare(output);
});

test('generation and check ignore inherited workspace and toolchain overrides', async () => {
    const generated = run(process.execPath, [join(directory, 'Matched/prepare.mjs')], directory, {
        ...process.env,
        GOWORK: join(output, 'missing.go.work'),
        GOTOOLCHAIN: 'invalid-toolchain'
    }).trim();
    await compare(generated);
});

test('subprocess execution returns stdout with the default timeout', () => {
    assert.equal(run(process.execPath, ['-e', 'process.stdout.write("completed")']), 'completed');
});

test('subprocess execution enforces an explicit timeout', () => {
    assert.throws(() => run(process.execPath, ['-e', 'setInterval(() => {}, 10000)'], directory, process.env, 100), /ETIMEDOUT/);
});

function recordGenerationSteps(t, elapsedPerStep = 0) {
    const calls = [];
    let elapsed = 0;
    const clock = t.mock.method(performance, 'now', () => elapsed);
    const spawn = t.mock.method(childProcess, 'spawnSync', (command, args, options) => {
        calls.push({ command, args, timeout: options.timeout });
        elapsed += elapsedPerStep;
        return { status: 0, stdout: '', stderr: '' };
    });
    syncBuiltinESMExports();
    t.after(() => {
        spawn.mock.restore();
        clock.mock.restore();
        syncBuiltinESMExports();
    });
    return calls;
}

test('ordinary generation retains a 60-second timeout for every Go step', async t => {
    const calls = recordGenerationSteps(t);
    await generate();
    assert.deepEqual(calls.map(call => call.timeout), Array(7).fill(60000));
});

test('cold generation passes the remaining shared budget to download, generation, tidy and list', async t => {
    const calls = recordGenerationSteps(t, 25000);
    await generate(180000);
    assert.deepEqual(calls.map(call => call.args.slice(0, 2)), [
        ['mod', 'download'], ['run', './cmd/arc-gen'], ['mod', 'tidy'], ['list', '-m'],
        ['mod', 'tidy'], ['list', '-m'], ['run', './cmd/arc-gen']
    ]);
    assert.deepEqual(calls.map(call => call.timeout), [180000, 155000, 130000, 105000, 80000, 55000, 30000]);
});

test('an exhausted generation deadline starts no further Go subprocess', async t => {
    const calls = recordGenerationSteps(t, 180000);
    await assert.rejects(generate(180000), /deadline exceeded before starting the next Go step/);
    assert.equal(calls.length, 1);
});

test('a timed-out generation step starts no further Go subprocess', async t => {
    const calls = [];
    const spawn = t.mock.method(childProcess, 'spawnSync', (command, args) => {
        calls.push(args.slice(0, 2));
        if (calls.length === 2) {
            return { error: new Error(`spawnSync ${command} ETIMEDOUT`), status: null, stdout: '', stderr: '' };
        }
        return { status: 0, stdout: '', stderr: '' };
    });
    syncBuiltinESMExports();
    t.after(() => {
        spawn.mock.restore();
        syncBuiltinESMExports();
    });
    await assert.rejects(generate(180000), /ETIMEDOUT/);
    assert.deepEqual(calls, [['mod', 'download'], ['run', './cmd/arc-gen']]);
});

// This fake Go has no descendants: the test proves only direct-child reaping,
// not termination of arc-gen or go list processes started by a real go run.
test('script timeout reaches and reaps the directly spawned Go process before returning', async () => {
    const { consumer } = await prepare();
    const go = join(consumer, 'slow-go.mjs');
    const pidFile = join(consumer, 'slow-go.pid');
    await writeFile(go, `#!${process.execPath}\nimport { writeFileSync } from 'node:fs';\nwriteFileSync(process.env.ARC_TEST_GO_PID, String(process.pid));\nsetInterval(() => {}, 10000);\n`, { mode: 0o700 });
    const env = { ...process.env, GO: go, ARC_TEST_GO_PID: pidFile, ARC_PROXY_COMPARISON_TIMEOUT_MS: '1000' };
    assert.throws(() => run(process.execPath, [join(directory, 'Matched/prepare.mjs')], directory, env, 5000), error => {
        assert.match(error.message, /ETIMEDOUT/);
        assert.ok(error.message.includes(`spawnSync ${go}`), 'The inner Go timeout must fail before the outer Node timeout');
        return true;
    });
    const pid = Number(await readFile(pidFile, 'utf8'));
    assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' }, 'The directly spawned Go process must be gone before returning');
});

// Allow a shared 180s for the ~59MB pinned graph over a slower proxy and all Go
// steps. The outer process gets 15s more to report the inner failure before cleanup.
const coldGenerationTimeout = 180000;
const coldProcessTimeout = coldGenerationTimeout + 15000;
test('generation and offline consumer tidy succeed with an empty module cache', { timeout: coldProcessTimeout + 30000 }, async t => {
    const cache = await mkdtemp(join(tmpdir(), 'arc-go-cold-module-cache-'));
    const env = { ...process.env, GOMODCACHE: cache, GOWORK: 'off', GOTOOLCHAIN: 'local',
        ARC_PROXY_COMPARISON_TIMEOUT_MS: String(coldGenerationTimeout) };
    // Go creates read-only module directories; clean them even when generation or assertions fail.
    t.after(async () => {
        run(process.env.GO || 'go', ['clean', '-modcache'], directory, env);
        await assert.rejects(access(cache), { code: 'ENOENT' }, 'Cold module cache must not survive the test');
    });
    const generated = run(process.execPath, [join(directory, 'Matched/prepare.mjs')], directory, env, coldProcessTimeout).trim();
    await compare(generated);
    const manifest = await readFile(join(generated, '../go.mod'), 'utf8');
    assert.match(manifest, /github\.com\/coder\/websocket v/);
    assert.doesNotMatch(manifest, /golang\.org\/x\/(tools|mod|sync)/);
    assert.equal(await readFile(toolsSum, 'utf8'), toolsChecksums, 'Generation must not modify the tools module checksums');
});

test('consumer tidy rejects an injected import unavailable in the pinned runtime graph', async () => {
    const prepared = await prepare();
    await writeFile(join(prepared.consumer, 'unexpected.go'), 'package consumer\nimport _ "example.invalid/outside-pinned-tools-graph"\n');
    assert.throws(() => tidyConsumer(prepared), /module lookup disabled by GOPROXY=off/);
    assert.doesNotMatch(await readFile(join(prepared.consumer, 'go.mod'), 'utf8'), /require example\.invalid/);
});

test('consumer tidy rejects an out-of-graph import even when it resolves offline', async () => {
    const prepared = await prepare();
    const dependency = join(prepared.consumer, 'unexpected-module');
    await mkdir(dependency);
    await writeFile(join(dependency, 'go.mod'), 'module example.invalid/outside-pinned-tools-graph\ngo 1.26.0\n');
    await writeFile(join(dependency, 'dependency.go'), 'package dependency\n');
    await writeFile(join(prepared.consumer, 'unexpected.go'), 'package consumer\nimport _ "example.invalid/outside-pinned-tools-graph"\n');
    await writeFile(join(prepared.consumer, 'go.mod'), await readFile(join(prepared.consumer, 'go.mod'), 'utf8') +
        '\nreplace example.invalid/outside-pinned-tools-graph => ./unexpected-module\n');
    assert.throws(() => tidyConsumer(prepared), /Consumer module is outside the pinned runtime graph: example\.invalid\/outside-pinned-tools-graph/);
});

test('consumer tidy rejects a tooling-only dependency already present in the pinned tools graph', async () => {
    const prepared = await prepare();
    await writeFile(join(prepared.consumer, 'unexpected.go'), 'package consumer\nimport _ "golang.org/x/mod/module"\n');
    // A warm module cache reaches the graph comparison; a cold one cannot load the
    // dependencies of the newly required module offline. Both reject the consumer.
    assert.throws(() => tidyConsumer(prepared), /Consumer module is outside the pinned runtime graph: golang\.org\/x\/mod |module lookup disabled by GOPROXY=off/);
});

test('every API fragment and every exact allowance rejects a new difference', async () => {
    for (const file of pairedFiles) {
        const csharp = fragments(await readFile(join(directory, 'Snapshots/Primary22.48.2', file)), 'csharp');
        const go = fragments(await readFile(join(output, file)), 'go');
        for (const key of Object.keys(go)) {
            assert.throws(() => compareFragments(file, csharp, { ...go, [key]: go[key] + ' unexpected' }, ledger.allowances), /Unapproved difference/);
        }
        assert.throws(() => compareFragments(file, csharp, { ...go, extraField: 'id : number' }, ledger.allowances), /Unapproved difference/);
        const allowance = ledger.allowances.find(entry => entry.file === file);
        if (allowance) {
            assert.throws(() => compareFragments(file, csharp, go, ledger.allowances.filter(entry => entry !== allowance)), /Unapproved difference/);
            assert.throws(() => compareFragments(file, csharp, go, [...ledger.allowances, allowance]), /difference|Stale or duplicate/);
        }
    }
});

test('unexpected output cannot be ignored by the inventory comparison', async () => {
    const extra = join(output, 'user.ts');
    await writeFile(extra, 'export const unexpected = true;\n');
    await assert.rejects(() => compare(output), /deep-equal/);
});

test('comments and trailing tokens are not blanket-normalized away', () => {
    const original = Buffer.from('// Code generated by arc-gen TypeScript models; DO NOT EDIT.\nexport class Model { id!: string; }\n');
    const commented = Buffer.from(original.toString().replace('id!', '// extra\n id!') + '// trailing\n');
    assert.notDeepEqual(fragments(original, 'go'), fragments(commented, 'go'));
    assert.notDeepEqual(fragments(original, 'go'), fragments(Buffer.from(original.toString().replace('id!', 'other!')), 'go'));
});

test('live Go output compiles strictly against the pinned real client packages', async () => {
    const nodeModules = join(directory, 'node_modules');
    // Normal package resolution must honor the packages' export maps, not load
    // their uncompiled source trees through a wildcard paths mapping.
    await symlink(nodeModules, join(output, '../node_modules'), 'dir');
    const program = ts.createProgram(pairedFiles.map(file => join(output, file)), {
        target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler,
        strict: true, skipLibCheck: false, noEmit: true, types: ['node'],
        typeRoots: [join(nodeModules, '@types')]
    });
    const diagnostics = ts.getPreEmitDiagnostics(program);
    assert.equal(diagnostics.length, 0, ts.formatDiagnosticsWithColorAndContext(diagnostics, {
        getCurrentDirectory: () => directory, getCanonicalFileName: file => file, getNewLine: () => '\n'
    }));
});
