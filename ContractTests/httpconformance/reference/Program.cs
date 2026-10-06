// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using System.Reflection;
using System.Text.Json;
using Cratis.Arc;
using Cratis.Arc.Queries;
using HttpConformance;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;

if (args is ["--provenance"])
{
    Console.WriteLine($"Microsoft.NETCore.App {Environment.Version}");
    Console.WriteLine($"Microsoft.AspNetCore.App {typeof(WebApplication).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()!.InformationalVersion}");
    return;
}

var negative = args is ["--readiness-negative", var witness] ? witness : null;
var builder = WebApplication.CreateBuilder(negative is null ? args : []);
builder.Logging.ClearProviders();
builder.WebHost.UseUrls("http://127.0.0.1:0");
builder.AddCratisArc(
    configureOptions: options =>
    {
        options.ExposeExceptionDetails = false;
        options.GeneratedApis.EnableQueryHttpMethod = true;
    },
    configureBuilder: arc => arc.WithoutControllers());
await using var app = builder.Build();
app.UseCratisArc();
await app.StartAsync();
object activation;
try
{
    activation = FixtureReadiness.Verify(app.Services.GetRequiredService<IQueryPerformerProviders>(), app, negative);
}
catch (InvalidOperationException exception)
{
    Console.Error.WriteLine(exception.Message);
    using var failedStartup = new CancellationTokenSource(TimeSpan.FromSeconds(5));
    await app.StopAsync(failedStartup.Token);
    Environment.ExitCode = 1;
    return;
}
var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.Single();
Console.WriteLine(JsonSerializer.Serialize(new { kind = "httpconformance-ready", baseUrl = address, activation }));
await Console.Out.FlushAsync();
// EOF is the owned, portable shutdown signal. There is no detached stdin worker.
using var input = Console.OpenStandardInput();
await input.CopyToAsync(Stream.Null);
using var shutdown = new CancellationTokenSource(TimeSpan.FromSeconds(5));
await app.StopAsync(shutdown.Token);
