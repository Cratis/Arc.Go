// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { directory, run } from '../helpers.mjs';

export async function prepare({ observable = false } = {}) {
    const root = resolve(directory, '../..');
    const scratch = process.env.AI_WORK_OUTPUT || join(root, '.ai-work/proxy-comparison');
    await mkdir(scratch, { recursive: true });
    const consumer = await mkdtemp(join(scratch, 'matched-'));
    const tools = join(root, 'tools');
    // Independent consumption of the existing tools module's fetchable runtime pin.
    // No workspace, local replace, extra published module or runtime dependency.
    await writeFile(join(consumer, 'go.mod'), (await readFile(join(tools, 'go.mod'), 'utf8'))
        .replace('module github.com/cratis/arc.go/tools', 'module example.test/proxy-comparison'));
    await writeFile(join(consumer, 'go.sum'), await readFile(join(tools, 'go.sum')));
    await writeFile(join(consumer, 'input.go'), await readFile(join(directory, 'Matched/input.go.txt')));
    await writeFile(join(consumer, 'profile.json'), JSON.stringify({ formatVersion: 1, name: 'matched',
        clientHttp: { 'ProxyComparison.Listing.All': 'Get' }, typescript: { out: 'web' } }));
    if (observable) await writeFile(join(consumer, 'observable.go'), await readFile(join(directory, 'Matched/observable.go.txt')));
    return { consumer, tools, go: process.env.GO || 'go' };
}

export async function generate() {
    const { consumer, tools, go } = await prepare();
    run(go, ['run', './cmd/arc-gen', '-dir', consumer, '-config', join(consumer, 'profile.json'), '.'], tools);
    // -check is production CLI verification, not a comparison with hand-written Go output.
    run(go, ['run', './cmd/arc-gen', '-dir', consumer, '-config', join(consumer, 'profile.json'), '-check', '.'], tools);
    return join(consumer, 'web');
}

if (process.argv[1] && resolve(process.argv[1]) === import.meta.filename) console.log(await generate());
