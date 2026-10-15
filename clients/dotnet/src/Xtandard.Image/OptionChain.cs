namespace Xtandard.Image;

/// <summary>What a fluent chain collected; the builder turns it into a URL.</summary>
internal sealed class ChainState(string source, Func<ChainState, string> run)
{
    internal string Source { get; } = source;

    internal List<List<OptionEntry>> Pipelines { get; } = [[]];

    internal List<KeyValuePair<string, OptionArg>> Params { get; } = [];

    internal SourceEncoding? Encoding { get; set; }

    internal string? Extension { get; set; }

    internal string? PdfPassword { get; set; }

    internal string Build() => run(this);
}

/// <summary>
/// The control methods every fluent URL builder shares: <see cref="Option"/>, <see cref="Build"/> and
/// <see cref="ToString"/>. The generated catalogue classes add one method per option name and alias.
/// </summary>
/// <typeparam name="TSelf">The concrete builder, returned by every method.</typeparam>
public abstract class OptionChain<TSelf>
    where TSelf : OptionChain<TSelf>
{
    private protected OptionChain(ChainState state)
    {
        State = state;
    }

    private protected ChainState State { get; }

    private TSelf Self => (TSelf)this;

    /// <summary>Appends one option occurrence by name or alias (as the per-option methods do).</summary>
    /// <param name="name">The option name or alias.</param>
    /// <param name="arguments">Its arguments; at least one.</param>
    /// <returns>This builder.</returns>
    public TSelf Option(string name, params OptionArg[] arguments)
    {
        ArgumentNullException.ThrowIfNull(name);
        ArgumentNullException.ThrowIfNull(arguments);
        State.Pipelines[^1].Add(new(name, [.. arguments]));
        return Self;
    }

    /// <summary>Builds the URL.</summary>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string Build() => State.Build();

    /// <summary>Builds the URL.</summary>
    /// <returns>The URL.</returns>
    public override string ToString() => Build();

    private protected TSelf AddOptions(IEnumerable<OptionEntry> options)
    {
        ArgumentNullException.ThrowIfNull(options);
        State.Pipelines[^1].AddRange(options);
        return Self;
    }

    private protected TSelf SetEncoding(SourceEncoding encoding)
    {
        State.Encoding = encoding;
        return Self;
    }

    private protected TSelf SetExtension(string extension)
    {
        ArgumentNullException.ThrowIfNull(extension);
        State.Extension = extension;
        return Self;
    }

    private protected TSelf NextPipeline()
    {
        State.Pipelines.Add([]);
        return Self;
    }

    private protected TSelf SetPdfPassword(string password)
    {
        ArgumentNullException.ThrowIfNull(password);
        State.PdfPassword = password;
        return Self;
    }
}

/// <content>Appends a whole options collection.</content>
public abstract partial class ProcessingChain<TSelf>
{
    /// <summary>Appends every option of an options collection, in order.</summary>
    /// <param name="options">The options.</param>
    /// <returns>This builder.</returns>
    public TSelf Options(ProcessingOptions options) => AddOptions(options);
}

/// <content>Appends a whole options collection.</content>
public abstract partial class InfoChain<TSelf>
{
    /// <summary>Appends every option of an options collection, in order.</summary>
    /// <param name="options">The options.</param>
    /// <returns>This builder.</returns>
    public TSelf Options(InfoOptions options) => AddOptions(options);
}

/// <content>Appends a whole options collection.</content>
public abstract partial class VideoChain<TSelf>
{
    /// <summary>Appends every option of an options collection, in order.</summary>
    /// <param name="options">The options.</param>
    /// <returns>This builder.</returns>
    public TSelf Options(VideoOptions options) => AddOptions(options);
}
