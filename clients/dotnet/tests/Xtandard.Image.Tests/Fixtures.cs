using System.Text.Json;

namespace Xtandard.Image.Tests;

/// <summary>The shared fixtures, read from the repository by relative path.</summary>
internal static class Fixtures
{
    private static readonly Lazy<string> Root = new(() =>
    {
        // bin/<config>/<tfm>/ → walk up to the directory that holds clients/spec.
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (File.Exists(Path.Combine(dir.FullName, "clients", "spec", "conformance.json")))
            {
                return dir.FullName;
            }
        }

        throw new InvalidOperationException("repository root (clients/spec/conformance.json) not found");
    });

    internal static readonly Lazy<JsonElement> Conformance = new(() => Load("clients/spec/conformance.json"));
    internal static readonly Lazy<JsonElement> Builder = new(() => Load("testdata/urls/imgproxy-builder.json"));
    internal static readonly Lazy<JsonElement> Vectors = new(() => Load("testdata/security/vectors.json"));

    private static JsonElement Load(string rel)
    {
        using var doc = JsonDocument.Parse(File.ReadAllText(Path.Combine(Root.Value, rel)));
        return doc.RootElement.Clone();
    }

    /// <summary>A JSON scalar as an option argument: integers stay integers, so 300 is not 300.0.</summary>
    internal static OptionArg Arg(JsonElement e) => e.ValueKind switch
    {
        JsonValueKind.String => e.GetString(),
        JsonValueKind.Number when e.TryGetInt64(out var l) => l,
        JsonValueKind.Number => e.GetDouble(),
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        JsonValueKind.Null => OptionArg.Null,
        _ => throw new InvalidOperationException($"unexpected argument {e}"),
    };

    internal static OptionArg[] Args(JsonElement array) => [.. array.EnumerateArray().Select(Arg)];

    internal static string? Str(this JsonElement e, string name) =>
        e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String ? v.GetString() : null;

    internal static bool Has(this JsonElement e, string name) =>
        e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind != JsonValueKind.Null;

    internal static JsonElement Get(this JsonElement e, string name) =>
        e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) ? v : default;

    internal static SourceEncoding Encoding(string name) => name switch
    {
        "base64" => SourceEncoding.Base64,
        "plain" => SourceEncoding.Plain,
        "encrypted" => SourceEncoding.Encrypted,
        _ => throw new InvalidOperationException($"unknown encoding {name}"),
    };

    internal static OptionNameStyle Style(string name) => name switch
    {
        "short" => OptionNameStyle.Short,
        "canonical" => OptionNameStyle.Canonical,
        "given" => OptionNameStyle.Given,
        _ => throw new InvalidOperationException($"unknown option name style {name}"),
    };
}
