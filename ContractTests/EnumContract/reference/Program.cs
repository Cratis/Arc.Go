// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using System.Globalization;
using System.Reflection;
using System.Text.Json;
using Cratis.Arc;

if (args.Length != 1) throw new ArgumentException("Supply the corpus path.");
if (Environment.Version.ToString() != "10.0.12") throw new InvalidOperationException("Requires runtime 10.0.12.");
var options = new JsonSerializerOptions().ConfigureArcDefaults();
var corpus = JsonSerializer.Deserialize<Corpus>(File.ReadAllText(args[0]), new JsonSerializerOptions { PropertyNameCaseInsensitive = true })!;
var reads = corpus.Reads.Select(input => Capture(input, options)).ToArray();
var writes = corpus.Writes.Select(input => Write(input, options)).ToArray();
var members = Types.All.ToDictionary(pair => pair.Key, pair => Enum.GetNames(pair.Value).Zip(Enum.GetValues(pair.Value).Cast<object>())
    .Select(pair => new { name = pair.First, value = Convert.ToInt64(pair.Second, CultureInfo.InvariantCulture).ToString(CultureInfo.InvariantCulture) }).ToArray());
Console.WriteLine(JsonSerializer.Serialize(new {
    runtime = Environment.Version.ToString(),
    arc = typeof(JsonSerializerOptionsConfiguration).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()!.InformationalVersion,
    fundamentals = typeof(Cratis.Json.EnumConverterFactory).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()!.InformationalVersion,
    members, reads, writes
}, new JsonSerializerOptions { WriteIndented = true }));

static object Capture(ReadInput input, JsonSerializerOptions options)
{
    try
    {
        var type = Types.All[input.Type];
        if (input.Nullable) type = typeof(Nullable<>).MakeGenericType(type);
        var value = JsonSerializer.Deserialize(input.Input, type, options);
        return new { input.ID, input.Type, input.Input, input.Nullable, accepted = true,
            value = value is null ? null : Convert.ToInt64(value, CultureInfo.InvariantCulture).ToString(CultureInfo.InvariantCulture),
            write = Encode(value, type, options), error = (string?)null };
    }
    catch (Exception error)
    {
        return new { input.ID, input.Type, input.Input, input.Nullable, accepted = false,
            value = (string?)null, write = (WriteResult?)null, error = error.GetType().Name };
    }
}

static object Write(WriteInput input, JsonSerializerOptions options)
{
    try
    {
        var type = Types.All[input.Type];
        var value = Enum.Parse(type, input.Value);
        return new { input.ID, input.Type, input.Value, accepted = true,
            output = JsonSerializer.Serialize(value, type, options), error = (string?)null };
    }
    catch (Exception error)
    {
        return new { input.ID, input.Type, input.Value, accepted = false, output = (string?)null, error = error.GetType().Name };
    }
}

static WriteResult Encode(object? value, Type type, JsonSerializerOptions options)
{
    try { return new(true, JsonSerializer.Serialize(value, type, options), null); }
    catch (Exception error) { return new(false, null, error.GetType().Name); }
}

record WriteResult(bool Accepted, string? Output, string? Error);
record Corpus(ReadInput[] Reads, WriteInput[] Writes);
record ReadInput(string ID, string Type, string Input, bool Nullable);
record WriteInput(string ID, string Type, string Value);

// Parse names are CLR declarations, not the TypeScript display/export names.
enum State : int { Zero = 0, Read = 1, Write = 4, Alias = 4, Negative = -2, High = 1 << 30, Sign = int.MinValue }
[Flags]
enum Access : int { None = 0, Read = 1, Write = 4, Alias = 4, High = 1 << 30, Sign = int.MinValue }
enum Wide : long { Small = 1, Unsafe = 9007199254740993 }
enum Unsigned : uint { Small = 1, High = uint.MaxValue }
enum Tiny : byte { Small = 1, High = byte.MaxValue }

static class Types
{
    public static readonly Dictionary<string, Type> All = new()
    {
        ["State"] = typeof(State), ["Access"] = typeof(Access), ["Wide"] = typeof(Wide),
        ["Unsigned"] = typeof(Unsigned), ["Tiny"] = typeof(Tiny)
    };
}
