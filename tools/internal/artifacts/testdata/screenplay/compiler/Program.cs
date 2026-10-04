// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using Cratis.Screenplay;
using Cratis.Screenplay.Printing;

if (args.Length != 1) throw new ArgumentException("Expected the metadata.play fixture path");
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
Console.WriteLine("Screenplay 4.48.1: shape assertions, compiler/printer round trip, Graph JSON rejection and undeclared-type diagnostic control passed");
