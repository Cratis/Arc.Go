---
title: Protect discovery endpoints
description: Expose command/query catalogs, identity schemas and development listings deliberately.
---

Discovery makes an application's callable surface inspectable without executing
its commands or queries. It can also reveal private names and development users;
configure its exposure separately from operation authorization.

## Exposure matrix

| Configuration | Authentication capability | Result |
| --- | --- | --- |
| Default Development | Any | Anonymous discovery |
| Default other environment | None | Discovery unmapped |
| Default other environment | Registered handlers | Authenticated discovery |
| RequireAuthentication true | None | Build failure |
| RequireAuthentication false | Any | Anonymous; production startup warning |
| Roles specified | Registered handlers | Authentication plus any listed role |
| Roles plus explicit anonymous | Any | Build failure |

Capability means explicitly registered authentication handlers. For embedded
credential verification, register `authentication.HostPrincipal()`. Arbitrary
outer middleware alone does not declare capability. Roles are exact and
case-sensitive. Anonymous protected discovery is empty 401; missing roles is
empty 403. Explicit credential failure remains 401 on public discovery.

## Routes and output

GET and HEAD map `/.cratis/commands`, `/.cratis/queries`,
`/.cratis/identity-details/schema`, `/.cratis/users` and `/.cratis/tenants` when
exposure is available. `Introspection.Enabled` disables only the two catalogs.
`/.cratis/me` is always mapped independently and requires an authenticated caller.

Lists and schemas are unwrapped JSON, not QueryResult. Catalog routes come from
the compiled table; `/validate` is not another command. Excluded artifacts remain
callable and retain authorization. Catalog membership never runs operation policies.
Command entries contain name, namespace, route, type, documentationSummary and
payloadSchema. Query entries also contain fullyQualifiedName and argumentsSchema.
Ordering is deterministic; summaries are explicit/generated metadata, empty by default.

`AddUsersProvider` and `AddTenantsProvider` register lazy resource-scoped providers.
Results concatenate in registration order without deduplication. No providers
returns `[]`. These values are display data, never credentials or membership.

Every discovery response uses `no-store, private` and merged `Vary: Cookie`.
This intentionally extends C# protection to catalogs and schemas. Continue with
[bounded schema support](schemas.md) when you expose custom model codecs.
