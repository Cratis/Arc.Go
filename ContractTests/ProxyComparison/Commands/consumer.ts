// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { Count, CreateTask, Echo, Identify, Many, Numbers, Register, Single, Toggle } from './Generated/Shop/Commands/index';
import type { Detail, IRegister } from './Generated/Shop/Commands/index';
import type { CommandResult } from '@cratis/arc/commands';
import type { SetCommandValues, ClearCommandValues } from '@cratis/arc.react/commands';
import { Guid } from '@cratis/fundamentals';

const content: IRegister = { id: Guid.empty, name: 'Name', enabled: false, quantity: 0, tags: [], 'EXACT-name': 'wire' };
const command = new Register();
command.setInitialValues(content);
command.description = undefined;
command['EXACT-name'] = 'quoted';
// @ts-expect-error numeric property is not a string
command.quantity = 'zero';
// @ts-expect-error exact wire name, not Go field name
command.Exact = 'wrong';
const registerHook: (initial?: IRegister) => [Register, SetCommandValues<IRegister>, ClearCommandValues] = Register.use;
const echo: Promise<CommandResult<string>> = new Echo().execute();
const count: Promise<CommandResult<number>> = new Count().validate();
const toggle: Promise<CommandResult<boolean>> = new Toggle().execute();
const identify: Promise<CommandResult<Guid>> = new Identify().execute();
const single: Promise<CommandResult<Detail>> = new Single().execute();
// Pinned C# template annotates list responses with the element generic. This is
// characterization, not a silent array annotation correction or typed-list claim.
const listAnnotation: Promise<CommandResult<Detail>> = new Many().execute();
// @ts-expect-error pinned annotation/runtime mismatch is intentionally retained
const correctedList: Promise<CommandResult<Detail[]>> = new Many().execute();
const numberListAnnotation: Promise<CommandResult<number>> = new Numbers().execute();
const create = new CreateTask();
create.title = 'real host';
void [registerHook, echo, count, toggle, identify, single, listAnnotation, correctedList, numberListAnnotation, create];
