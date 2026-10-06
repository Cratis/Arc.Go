// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { DateOnly, Guid, TimeOnly, TimeSpan } from '@cratis/fundamentals';
import { Access, allAccess, Detail, Notice, Scalars, State, UrgentNotice } from './Generated/Shop/Models';

const model = new Scalars();
model.id = Guid.parse('12345678-90ab-cdef-0123-456789abcdef');
model.day = DateOnly.parse('2026-01-02');
model.startsAt = TimeOnly.parse('03:04:05.123');
model.duration = TimeSpan.parse('-2.03:04:05.1234567');
model.created = new Date('2026-01-02T03:04:05Z');
model.state = State.published;
model.states = [State.draft, State.default];
model.counts = { first: 0 };
model.access = allAccess;
model.details = [new Detail()];
model.ids = [model.id];
model.description = undefined;
model.nullableId = undefined;
model.nullableDetail = new Detail();
model['EXACT-name'] = 'preserved';
model.URL = 'preserved';
model.notice = new UrgentNotice();
const notice: Notice = model.notice;
const optionalName: string | undefined = model.description;
const optionalID: Guid | undefined = model.nullableId;
const optionalModel: Detail | undefined = model.nullableDetail;
const enumValue: Access = model.access;
void [notice, optionalName, optionalID, optionalModel, enumValue];

// @ts-expect-error Guid fields must not degrade to string/any.
model.id = 'invalid';
// @ts-expect-error DateOnly must not degrade to Date/string/any.
model.day = new Date();
// @ts-expect-error Unknown numeric enum values are not a widened number API.
model.state = 101;
// @ts-expect-error Nested models preserve their declared fields.
model.details = [{ label: 'missing id' }];
// @ts-expect-error Preserve C# nullable presentation (optional, not an added null API).
model.nullableId = null;
