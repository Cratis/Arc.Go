// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { chmodSync, existsSync, statSync, writeFileSync } from 'node:fs';
import { access, mkdir, readFile, symlink, writeFile } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';
import { performance } from 'node:perf_hooks';
import test, { after } from 'node:test';
import ts from 'typescript';
import { compare, compareFragments, fragments, pairedFiles } from './compare.mjs';
import { generate, prepare, tidyConsumer } from './Matched/prepare.mjs';
import { createModuleCache, directory, removeModuleCache, run } from './helpers.mjs';

const moduleCache = createModuleCache();
const goEnv = { ...process.env, GOMODCACHE: moduleCache };
after(() => removeModuleCache(moduleCache));

const toolsSum = join(directory, '../../tools/go.sum');
const toolsChecksums = await readFile(toolsSum, 'utf8');
const output = await generate(undefined, goEnv);
const ledger = JSON.parse(await readFile(join(directory, 'Matched/allowances.json')));

test('production arc-gen output compares file-by-file against untouched pinned C# captures', async () => {
    await compare(output);
});

test('generation and check ignore inherited workspace and toolchain overrides', async () => {
    const generated = run(process.execPath, [join(directory, 'Matched/prepare.mjs')], directory, {
        ...goEnv,
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

test('subprocess execution refuses Go cleanup before spawning any executable', t => {
    const spawn = t.mock.method(childProcess, 'spawnSync', () => assert.fail('Cleanup must not spawn'));
    syncBuiltinESMExports();
    t.after(() => {
        spawn.mock.restore();
        syncBuiltinESMExports();
    });
    for (const command of ['go', '/usr/local/bin/go', '/test/custom-go-wrapper']) {
        for (const args of [['clean'], ['clean', '-modcache'], ['-C', directory, 'clean', '-modcache']]) {
            assert.throws(() => run(command, args), /Refusing to run go clean/);
        }
    }
    assert.equal(spawn.mock.callCount(), 0);
});

test('module cache cleanup refuses paths outside the owned test temp directory', () => {
    for (const path of [directory, join(homedir(), 'go/pkg/mod'), tmpdir(),
        join(tmpdir(), 'arc-go-proxy-test-module-cache-unowned'), join(moduleCache, 'nested')]) {
        assert.throws(() => removeModuleCache(path), /outside the owned test temp module cache/);
    }
    assert.ok(existsSync(moduleCache));
});

test('module cache cleanup removes read-only files without following symlinks', async t => {
    const cache = createModuleCache();
    const target = join(moduleCache, 'symlink-target');
    writeFileSync(target, 'must survive', { mode: 0o400 });
    await mkdir(join(cache, 'module'));
    writeFileSync(join(cache, 'module/file.go'), 'package module', { mode: 0o400 });
    chmodSync(join(cache, 'module'), 0o500);
    await symlink(target, join(cache, 'external-file'));
    await symlink(moduleCache, join(cache, 'external-directory'));
    t.after(() => { if (existsSync(cache)) removeModuleCache(cache); });
    removeModuleCache(cache);
    assert.ok(!existsSync(cache));
    assert.equal(await readFile(target, 'utf8'), 'must survive');
    assert.equal(statSync(target).mode & 0o777, 0o400);
});

function recordGenerationSteps(t, elapsedPerStep = 0) {
    const calls = [];
    let elapsed = 0;
    const clock = t.mock.method(performance, 'now', () => elapsed);
    const spawn = t.mock.method(childProcess, 'spawnSync', (command, args, options) => {
        calls.push({ command, args, timeout: options.timeout, env: options.env });
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
    await generate(undefined, goEnv);
    assert.deepEqual(calls.map(call => call.timeout), Array(7).fill(60000));
    assert.ok(calls.every(call => call.env.GOMODCACHE === moduleCache));
});

test('cold generation passes the remaining shared budget to download, generation, tidy and list', async t => {
    const calls = recordGenerationSteps(t, 25000);
    const cache = createModuleCache();
    t.after(() => removeModuleCache(cache));
    await generate(180000, { ...goEnv, GOMODCACHE: cache });
    assert.ok(calls.every(call => call.env.GOMODCACHE === cache));
    assert.deepEqual(calls.map(call => call.args.slice(0, 2)), [
        ['mod', 'download'], ['run', './cmd/arc-gen'], ['mod', 'tidy'], ['list', '-m'],
        ['mod', 'tidy'], ['list', '-m'], ['run', './cmd/arc-gen']
    ]);
    assert.deepEqual(calls.map(call => call.timeout), [180000, 155000, 130000, 105000, 80000, 55000, 30000]);
});

test('an exhausted generation deadline starts no further Go subprocess', async t => {
    const calls = recordGenerationSteps(t, 180000);
    await assert.rejects(generate(180000, goEnv), /deadline exceeded before starting the next Go step/);
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
    await assert.rejects(generate(180000, goEnv), /ETIMEDOUT/);
    assert.deepEqual(calls, [['mod', 'download'], ['run', './cmd/arc-gen']]);
});

// This fake Go has no descendants: the test proves only direct-child reaping,
// not termination of arc-gen or go list processes started by a real go run.
test('script timeout reaches and reaps the directly spawned Go process before returning', async () => {
    const { consumer } = await prepare(goEnv);
    const go = join(consumer, 'slow-go.mjs');
    const pidFile = join(consumer, 'slow-go.pid');
    await writeFile(go, `#!${process.execPath}\nimport { writeFileSync } from 'node:fs';\nwriteFileSync(process.env.ARC_TEST_GO_PID, String(process.pid));\nsetInterval(() => {}, 10000);\n`, { mode: 0o700 });
    const env = { ...goEnv, GO: go, ARC_TEST_GO_PID: pidFile, ARC_PROXY_COMPARISON_TIMEOUT_MS: '1000' };
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
    const cache = createModuleCache();
    const env = { ...goEnv, GOMODCACHE: cache, GOWORK: 'off', GOTOOLCHAIN: 'local',
        ARC_PROXY_COMPARISON_TIMEOUT_MS: String(coldGenerationTimeout) };
    // Cleanup never invokes Go and can only remove the cache this test created.
    t.after(async () => {
        removeModuleCache(cache);
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
    const prepared = await prepare(goEnv);
    await writeFile(join(prepared.consumer, 'unexpected.go'), 'package consumer\nimport _ "example.invalid/outside-pinned-tools-graph"\n');
    assert.throws(() => tidyConsumer(prepared), /module lookup disabled by GOPROXY=off/);
    assert.doesNotMatch(await readFile(join(prepared.consumer, 'go.mod'), 'utf8'), /require example\.invalid/);
});

test('consumer tidy rejects an out-of-graph import even when it resolves offline', async () => {
    const prepared = await prepare(goEnv);
    const dependency = join(prepared.consumer, 'unexpected-module');
    await mkdir(dependency);
    await writeFile(join(dependency, 'go.mod'), 'module example.invalid/outside-pinned-tools-graph\ngo 1.26.0\n');
    await writeFile(join(dependency, 'dependency.go'), 'package dependency\n');
    await writeFile(join(prepared.consumer, 'unexpected.go'), 'package consumer\nimport _ "example.invalid/outside-pinned-tools-graph"\n');
    await writeFile(join(prepared.consumer, 'go.mod'), await readFile(join(prepared.consumer, 'go.mod'), 'utf8') +
        '\nreplace example.invalid/outside-pinned-tools-graph => ./unexpected-module\n');
    assert.throws(() => tidyConsumer(prepared), /Consumer module is outside the pinned runtime graph: example\.invalid\/outside-pinned-tools-graph/);
});

async function warmToolingGraph(prepared) {
    // The pruned tools graph does not cache go.mod files for x/mod's own requirements.
    // Warm that graph online in a test-only modfile so offline list reaches the guard.
    const modfile = join(prepared.consumer, 'tooling.mod');
    await writeFile(modfile, 'module example.test/tooling-graph\ngo 1.26.0\nrequire golang.org/x/mod v0.41.0\n');
    const env = { ...prepared.env };
    run(prepared.go, ['mod', 'download', '-modfile', modfile, 'all'], prepared.consumer, env);
}

test('tooling graph warm-up preserves the caller-configured Go proxy', async t => {
    const proxy = 'https://mirror.example.test/go,https://fallback.example.test/go';
    const originalProxy = process.env.GOPROXY;
    process.env.GOPROXY = proxy;
    const calls = [];
    const spawn = t.mock.method(childProcess, 'spawnSync', (command, args, options) => {
        calls.push({ command, args, cwd: options.cwd, env: options.env });
        return { status: 0, stdout: '', stderr: '' };
    });
    syncBuiltinESMExports();
    t.after(() => {
        if (originalProxy === undefined) delete process.env.GOPROXY;
        else process.env.GOPROXY = originalProxy;
        spawn.mock.restore();
        syncBuiltinESMExports();
    });
    const prepared = await prepare({ ...goEnv, GOPROXY: proxy });
    await warmToolingGraph(prepared);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].command, prepared.go);
    assert.deepEqual(calls[0].args, ['mod', 'download', '-modfile', join(prepared.consumer, 'tooling.mod'), 'all']);
    assert.equal(calls[0].cwd, prepared.consumer);
    assert.equal(calls[0].env.GOPROXY, proxy);
    assert.equal(calls[0].env.GOMODCACHE, moduleCache);
    assert.equal(calls[0].env.GOWORK, 'off');
    assert.equal(calls[0].env.GOTOOLCHAIN, 'local');
    assert.equal(process.env.GOPROXY, proxy, 'Warm-up must not mutate the caller environment');
});

test('consumer tidy rejects a tooling-only dependency already present in the pinned tools graph', async () => {
    const prepared = await prepare(goEnv);
    const manifest = join(prepared.consumer, 'go.mod');
    const originalPins = await readFile(manifest, 'utf8');
    tidyConsumer(prepared);
    // The control prunes unused tooling pins; restore the authored graph before injection.
    await writeFile(manifest, originalPins);
    await warmToolingGraph(prepared);
    await writeFile(join(prepared.consumer, 'unexpected.go'), 'package consumer\nimport _ "golang.org/x/mod/module"\n');
    assert.throws(() => tidyConsumer(prepared), /Consumer module is outside the pinned runtime graph: golang\.org\/x\/mod v0\.41\.0/);
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
