// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

// A fresh, task-owned directory only. Refuse reuse rather than deleting evidence
// from a previous failed generation or overwriting someone's scratch consumer.
const root = new URL('../../../.ai-work/output/ts-publication/', import.meta.url);
await mkdir(new URL('../', root), { recursive: true });
await mkdir(root);
const consumer = new URL('consumer/', root);
await mkdir(new URL('shared/', consumer), { recursive: true });
await mkdir(new URL('host/', consumer));
const tools = new URL('../../../tools/', import.meta.url);
const manifest = (await readFile(new URL('go.mod', tools), 'utf8')).replace(
    'module github.com/cratis/arc.go/tools', 'module example.test/consumer');
await writeFile(new URL('go.mod', consumer), manifest);
await writeFile(new URL('go.sum', consumer), await readFile(new URL('go.sum', tools)));
for (const [source, destination] of [['input.go.txt', 'input.go'], ['shared.go.txt', 'shared/shared.go'],
    ['host.go.txt', 'host/main.go'], ['contract_test.go.txt', 'contract_test.go'], ['profile.json', 'profile.json']]) {
    await writeFile(new URL(destination, consumer), await readFile(new URL(source, import.meta.url)));
}
await writeFile(new URL('package.json', consumer), '{"type":"module"}\n');
console.log(fileURLToPath(consumer));
