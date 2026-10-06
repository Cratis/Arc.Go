// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { Search, SearchValidator } from './Generated/Shop/Validation/Search';
import type { SearchParameters } from './Generated/Shop/Validation/Search';

const optional: SearchParameters = { required: 'yes' };
const all: SearchParameters = { required: 'yes', 'EXACT-name': 'abc', present: 'yes', range: 'ab', warning: 'a', info: 'ab' };
new SearchValidator().validate(optional);
new SearchValidator().validate(all);
const query = new Search();
query.parameters = all;
void query.perform(all);
// @ts-expect-error wire presence remains required independently of validator rules
const missingRequired: SearchParameters = {};
void missingRequired;
