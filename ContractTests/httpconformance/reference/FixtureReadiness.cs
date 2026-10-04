// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using Cratis.Arc.Queries;
using Microsoft.AspNetCore.Routing;

namespace HttpConformance;

internal static class FixtureReadiness
{
    internal static object Verify(IQueryPerformerProviders providers, IEndpointRouteBuilder routes, string? negative)
    {
        var expected = new Dictionary<string, string>(StringComparer.Ordinal)
        {
            ["Plain"] = "/api/plain",
            ["Renderable"] = "/api/renderable",
            ["Filter"] = "/api/filter",
            ["Failing"] = "/api/failing"
        };
        var performers = providers.Performers.Where(performer => performer.ReadModelType == typeof(Row)).ToArray();
        var endpoints = routes.DataSources.SelectMany(source => source.Endpoints).OfType<RouteEndpoint>()
            .Where(endpoint => expected.Values.Contains(endpoint.RoutePattern.RawText, StringComparer.Ordinal) ||
                endpoint.Metadata.GetMetadata<IEndpointNameMetadata>()?.EndpointName is { } name &&
                (name.StartsWith("ExecuteHttpConformance.Row.", StringComparison.Ordinal) ||
                 name.StartsWith("QueryHttpConformance.Row.", StringComparison.Ordinal)))
            .ToArray();

        // Negative witnesses remove observations, never replace Arc's registrations or request handlers.
        if (negative == "missing-performer") performers = performers.Where(performer => performer.Name.ToString() != "Plain").ToArray();
        else if (negative == "missing-query") endpoints = endpoints.Where(endpoint => endpoint.Metadata.GetMetadata<IEndpointNameMetadata>()?.EndpointName != "QueryHttpConformance.Row.Plain").ToArray();
        else if (negative is not null) throw new InvalidOperationException($"Unknown readiness witness: {negative}");

        if (performers.Length != expected.Count) throw new InvalidOperationException($"Fixture readiness: expected 4 Row performers; got {performers.Length}.");
        foreach (var (method, path) in expected)
        {
            var matches = performers.Where(performer => performer.Name.ToString() == method).ToArray();
            if (matches.Length != 1 || matches[0].FullyQualifiedName.ToString() != $"HttpConformance.Row.{method}" || matches[0].CustomRoute != path)
                throw new InvalidOperationException($"Fixture readiness: missing, duplicate or wrong Row performer {method} at {path}.");
        }
        if (endpoints.Length != 8) throw new InvalidOperationException($"Fixture readiness: expected 8 Row endpoints; got {endpoints.Length}.");
        foreach (var (method, path) in expected)
        {
            foreach (var (verb, prefix) in new[] { ("GET", "Execute"), ("QUERY", "Query") })
            {
                var name = $"{prefix}HttpConformance.Row.{method}";
                var matches = endpoints.Where(endpoint => endpoint.Metadata.GetMetadata<IEndpointNameMetadata>()?.EndpointName == name).ToArray();
                if (matches.Length != 1 || matches[0].RoutePattern.RawText != path ||
                    matches[0].Metadata.GetMetadata<IHttpMethodMetadata>()?.HttpMethods is not { Count: 1 } verbs || verbs[0] != verb)
                    throw new InvalidOperationException($"Fixture readiness: missing, duplicate or wrong endpoint {name} ({verb} {path}).");
            }
        }
        return new
        {
            performers = performers.Select(performer => new { name = performer.Name.ToString(), fullyQualifiedName = performer.FullyQualifiedName.ToString(), path = performer.CustomRoute }).OrderBy(performer => performer.name, StringComparer.Ordinal),
            endpoints = endpoints.Select(endpoint => new { name = endpoint.Metadata.GetMetadata<IEndpointNameMetadata>()!.EndpointName, path = endpoint.RoutePattern.RawText, method = endpoint.Metadata.GetMetadata<IHttpMethodMetadata>()!.HttpMethods.Single() }).OrderBy(endpoint => endpoint.name, StringComparer.Ordinal)
        };
    }
}
