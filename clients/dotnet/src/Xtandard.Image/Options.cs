using System.Collections;
using System.Collections.Frozen;
using System.Text;

namespace Xtandard.Image;

/// <summary>How option names are written.</summary>
public enum OptionNameStyle
{
    /// <summary>The shortest alias (<c>rs</c>, <c>q</c>): the default.</summary>
    [System.Diagnostics.CodeAnalysis.SuppressMessage("Naming", "CA1720:Identifier contains type name", Justification = "The style is named short in every official client.")]
    Short,

    /// <summary>The canonical name (<c>resize</c>, <c>quality</c>).</summary>
    Canonical,

    /// <summary>The name or alias as given.</summary>
    Given,
}

/// <summary>One option occurrence: a name (canonical or alias) and its arguments.</summary>
/// <param name="Name">The option name or alias.</param>
/// <param name="Arguments">The positional arguments.</param>
public readonly record struct OptionEntry(string Name, IReadOnlyList<OptionArg> Arguments);

/// <summary>The base of the typed option records: a canonical option name and its arguments.</summary>
public abstract record OptionValue
{
    private readonly OptionArg[] arguments;

    private protected OptionValue(string name, OptionArg[] arguments)
    {
        Name = name;
        this.arguments = arguments;
    }

    /// <summary>The canonical option name.</summary>
    public string Name { get; }

    /// <summary>The positional arguments.</summary>
    public IReadOnlyList<OptionArg> Arguments => arguments;

    /// <summary>The option as an entry of an options collection.</summary>
    /// <returns>The entry.</returns>
    public OptionEntry ToEntry() => new(Name, arguments);

    /// <inheritdoc/>
    public virtual bool Equals(OptionValue? other) =>
        other is not null && EqualityContract == other.EqualityContract && Name == other.Name && arguments.AsSpan().SequenceEqual(other.arguments);

    /// <inheritdoc/>
    public override int GetHashCode()
    {
        var h = new HashCode();
        h.Add(EqualityContract);
        h.Add(Name);
        foreach (var a in arguments)
        {
            h.Add(a);
        }

        return h.ToHashCode();
    }

    /// <summary>Prints the option as written with canonical names: <c>resize:fill:300:400</c>.</summary>
    /// <param name="builder">The builder.</param>
    /// <returns>True.</returns>
    protected virtual bool PrintMembers(StringBuilder builder)
    {
        ArgumentNullException.ThrowIfNull(builder);
        builder.Append(Name);
        foreach (var a in arguments)
        {
            builder.Append(':').Append(a.ToString());
        }

        return true;
    }
}

/// <summary>
/// A typed processing option. The nested records are the catalogue: <c>new ProcessingOption.Resize("fill", 300, 400)</c>,
/// or with <c>using static Xtandard.Image.ProcessingOption;</c>, <c>new Resize("fill", 300, 400)</c>.
/// </summary>
public abstract partial record ProcessingOption : OptionValue
{
    private protected ProcessingOption(string name, OptionArg[] arguments)
        : base(name, arguments)
    {
    }
}

/// <summary>A typed <c>/info</c> option; the nested records are the catalogue.</summary>
public abstract partial record InfoOption : OptionValue
{
    private protected InfoOption(string name, OptionArg[] arguments)
        : base(name, arguments)
    {
    }
}

/// <summary>A typed <c>/video</c> option; the nested records are the catalogue.</summary>
public abstract partial record VideoOption : OptionValue
{
    private protected VideoOption(string name, OptionArg[] arguments)
        : base(name, arguments)
    {
    }
}

/// <summary>
/// The options of one pipeline, in URL order. A collection initializer takes option names or aliases
/// with their arguments, or typed option records:
/// <code>new ProcessingOptions { { "rs", "fill", 300, 400 }, { "q", 80 }, new ProcessingOption.Blur(2) }</code>
/// </summary>
/// <typeparam name="TOption">The typed option records this catalogue accepts.</typeparam>
public abstract class OptionCollection<TOption> : IReadOnlyList<OptionEntry>
    where TOption : OptionValue
{
    private readonly List<OptionEntry> entries = [];

    /// <summary>The number of options.</summary>
    public int Count => entries.Count;

    /// <summary>The option at an index.</summary>
    /// <param name="index">The index.</param>
    public OptionEntry this[int index] => entries[index];

    /// <summary>Appends an option by name or alias.</summary>
    /// <param name="name">The option name or alias.</param>
    /// <param name="arguments">Its arguments; at least one.</param>
    public void Add(string name, params OptionArg[] arguments)
    {
        ArgumentNullException.ThrowIfNull(name);
        ArgumentNullException.ThrowIfNull(arguments);
        entries.Add(new(name, [.. arguments]));
    }

    /// <summary>Appends a typed option.</summary>
    /// <param name="option">The option.</param>
    public void Add(TOption option)
    {
        ArgumentNullException.ThrowIfNull(option);
        entries.Add(option.ToEntry());
    }

    /// <summary>Appends an option entry.</summary>
    /// <param name="entry">The entry.</param>
    public void Add(OptionEntry entry)
    {
        ArgumentNullException.ThrowIfNull(entry.Name);
        entries.Add(entry);
    }

    /// <inheritdoc/>
    public IEnumerator<OptionEntry> GetEnumerator() => entries.GetEnumerator();

    IEnumerator IEnumerable.GetEnumerator() => GetEnumerator();
}

/// <summary>Processing options of one pipeline, in URL order.</summary>
public sealed class ProcessingOptions : OptionCollection<ProcessingOption>;

/// <summary><c>/info</c> options, in URL order.</summary>
public sealed class InfoOptions : OptionCollection<InfoOption>;

/// <summary><c>/video</c> options, in URL order.</summary>
public sealed class VideoOptions : OptionCollection<VideoOption>;

/// <summary>One option catalogue at run time: names and aliases to canonical, canonical to short.</summary>
internal sealed class Catalogue(string kind, FrozenDictionary<string, string> names, FrozenDictionary<string, string> shortNames)
{
    internal static readonly Catalogue Processing = new("imgproxy", OptionCatalog.ProcessingNames, OptionCatalog.ProcessingShortNames);
    internal static readonly Catalogue Info = new("info", OptionCatalog.InfoNames, OptionCatalog.InfoShortNames);
    internal static readonly Catalogue Video = new("video", OptionCatalog.VideoNames, OptionCatalog.VideoShortNames);

    /// <summary>The canonical name of a name or alias, or null.</summary>
    internal string? Canonical(string name) => names.GetValueOrDefault(name);

    /// <summary>The name an option is written with.</summary>
    internal string WrittenName(string name, OptionNameStyle style)
    {
        var canonical = names.GetValueOrDefault(name)
            ?? throw new UrlBuildException(UrlBuildErrorCode.UnknownOption, $"unknown {kind} option '{name}'");
        return style switch
        {
            OptionNameStyle.Given => name,
            OptionNameStyle.Canonical => canonical,
            _ => shortNames.GetValueOrDefault(canonical, canonical),
        };
    }

    /// <summary>Formats one option segment (<c>rs:fill:300:400</c>).</summary>
    internal string FormatEntry(OptionEntry entry, string separator, OptionNameStyle style)
    {
        var written = WrittenName(entry.Name, style);
        if (entry.Arguments.Count == 0)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidArgument, $"option {entry.Name}: at least one argument is required");
        }

        var sb = new StringBuilder(written);
        for (var i = 0; i < entry.Arguments.Count; i++)
        {
            sb.Append(separator).Append(entry.Arguments[i].FormatOption(separator, $"option {entry.Name} argument {i + 1}"));
        }

        return sb.ToString();
    }
}
