// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using Cratis.Screenplay;
using Cratis.Screenplay.Diagnostics;
using Cratis.Screenplay.Printing;

if (args.Length is < 1 or > 2) throw new ArgumentException("Expected metadata.play and optional interleaved.play fixture paths");
var compiler = new ScreenplayCompiler();
var document = File.ReadAllText(args[0]);
var compiled = compiler.Compile(document);
if (!compiled.Success || compiled.Diagnostics.Any())
    throw new InvalidOperationException(string.Join(Environment.NewLine, compiled.Diagnostics));
var application = compiled.Value!;
var slices = application.Modules.Single().Features.Single().Slices.ToArray();
if (application.Domain?.Name != "Tasks" || application.Types?.Count() != 3 || slices.Length != 2)
    throw new InvalidOperationException("Wrong application shape");
var command = slices.SelectMany(slice => slice.Commands).Single();
if (command.Name != "Register" || command.Properties.Count() != 2 || command.Properties.First().Name != "authorize")
    throw new InvalidOperationException("Escaped command properties were not preserved");
var readModel = slices.SelectMany(slice => slice.ReadModels ?? []).Single();
if (readModel.Name != "Listing" || readModel.Properties.Count() != 3)
    throw new InvalidOperationException("Read-model shape was lost");
var queries = slices.SelectMany(slice => slice.Queries).ToArray();
if (queries.Length != 2 || !queries[0].ReturnType.IsCollection || !queries[1].IsObservable || !queries[1].ReturnType.IsOptional || queries[1].Filters.Single().Name != "prefix")
    throw new InvalidOperationException("Query shape was lost");
var printed = new ScreenplayPrinter().Print(application);
var roundTrip = compiler.Compile(printed);
if (!roundTrip.Success || roundTrip.Diagnostics.Any())
    throw new InvalidOperationException("Compiler/printer round trip failed");
if (compiler.Compile("{\"formatVersion\":2,\"commands\":[]}").Success)
    throw new InvalidOperationException("Negative Graph JSON control was accepted");
// The pinned compiler reports unresolved types as warnings, not Success=false.
// This witness requires zero diagnostics, a stronger and explicitly local gate.
var unknown = compiler.Compile(document.Replace("filter prefix String", "filter prefix MissingType"));
if (!unknown.Diagnostics.Any(diagnostic => diagnostic.ToString().Contains("MissingType", StringComparison.Ordinal)))
    throw new InvalidOperationException("Undeclared-type diagnostic control did not diagnose MissingType");
// Empty models, including no-input commands' model descriptors, cannot be
// represented as a bare type in this pinned grammar. The Go exporter refuses
// these inputs without bytes instead of adding invented properties.
foreach (var name in new[] { "EmptyModel", "Register" })
{
    var empty = compiler.Compile($"domain Tasks\n\ntype {name}\n");
    if (empty.Success || !empty.Diagnostics.Any(diagnostic => diagnostic.Code == DiagnosticCodes.TypeWithoutProperties))
        throw new InvalidOperationException($"Empty type {name} did not fail with TypeWithoutProperties");
}
Console.WriteLine("Screenplay 4.48.1: shape assertions, compiler/printer round trip, Graph JSON rejection, undeclared-type and empty-type diagnostic controls passed");

if (args.Length == 2)
{
    var interleaved = compiler.Compile(File.ReadAllText(args[1]));
    if (!interleaved.Success || interleaved.Diagnostics.Any())
        throw new InvalidOperationException(string.Join(Environment.NewLine, interleaved.Diagnostics));
    var grouped = interleaved.Value!;
    var groupedSlices = grouped.Modules.Single().Features.Single().Slices.ToArray();
    if (grouped.Domain?.Name != "Tasks" || grouped.Types?.Count() != 3 || groupedSlices.Length != 3)
        throw new InvalidOperationException("Wrong namespace-interleaving application shape");
    var views = groupedSlices.Where(slice => (slice.ReadModels ?? []).Any()).ToArray();
    if (views.Length != 2 || views[0].ReadModels!.Single().Name != "Listing" || views[1].ReadModels!.Single().Name != "Other" ||
        !views[0].Queries.Select(query => query.Name).SequenceEqual(new[] { "A", "Z" }) ||
        !views[1].Queries.Select(query => query.Name).SequenceEqual(new[] { "All" }) ||
        views[0].Queries.Any(query => query.ReturnType.Name != "Listing") || views[1].Queries.Single().ReturnType.Name != "Other")
        throw new InvalidOperationException("Namespace-interleaved queries lost their read-model groups");
    var groupedRoundTrip = compiler.Compile(new ScreenplayPrinter().Print(grouped));
    if (!groupedRoundTrip.Success || groupedRoundTrip.Diagnostics.Any())
        throw new InvalidOperationException("Namespace-interleaving compiler/printer round trip failed");
    Console.WriteLine("Screenplay 4.48.1: namespace-interleaving shape, unique read-model groups, stable query order and compiler/printer round trip passed");
}
