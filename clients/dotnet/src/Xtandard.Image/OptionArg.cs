using System.Globalization;

namespace Xtandard.Image;

/// <summary>
/// One option argument as written in a URL: a string, an integer, a number, a boolean or
/// <see cref="Null"/> (an empty argument, as in <c>rs:fill:300::1</c>).
/// </summary>
/// <remarks>
/// Strings, integers, numbers and booleans convert implicitly, so <c>Option("rs", "fill", 300, 400)</c>
/// works. Numbers are written as ECMAScript's <c>Number::toString</c> writes them (<c>300</c>,
/// <c>0.1</c>, <c>1e-7</c>), booleans as <c>1</c>/<c>0</c> in options and <c>true</c>/<c>false</c>
/// in native query keys. Strings are checked, never escaped.
/// </remarks>
public readonly record struct OptionArg
{
    private enum ArgKind : byte
    {
        Null,
        String,
        Integer,
        Number,
        Boolean,
    }

    private readonly ArgKind kind;
    private readonly string? text;
    private readonly long integer;
    private readonly double number;

    private OptionArg(ArgKind kind, string? text = null, long integer = 0, double number = 0)
    {
        this.kind = kind;
        this.text = text;
        this.integer = integer;
        this.number = number;
    }

    /// <summary>The empty argument (also <c>default(OptionArg)</c>).</summary>
    public static OptionArg Null => default;

    /// <summary>Whether this is the empty argument.</summary>
    public bool IsNull => kind == ArgKind.Null;

    /// <summary>A string argument; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    /// <returns>The argument.</returns>
    public static OptionArg FromString(string? value) => value is null ? default : new(ArgKind.String, text: value);

    /// <summary>An integer argument; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    /// <returns>The argument.</returns>
    public static OptionArg FromInt64(long? value) => value is { } v ? new(ArgKind.Integer, integer: v) : default;

    /// <summary>A number argument; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    /// <returns>The argument.</returns>
    public static OptionArg FromDouble(double? value) => value is { } v ? new(ArgKind.Number, number: v) : default;

    /// <summary>A boolean argument; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    /// <returns>The argument.</returns>
    public static OptionArg FromBoolean(bool? value) => value is { } v ? new(ArgKind.Boolean, integer: v ? 1 : 0) : default;

    /// <summary>Converts a string; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(string? value) => FromString(value);

    /// <summary>Converts an integer.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(int value) => FromInt64(value);

    /// <summary>Converts an integer; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(int? value) => FromInt64(value);

    /// <summary>Converts an integer.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(long value) => FromInt64(value);

    /// <summary>Converts an integer; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(long? value) => FromInt64(value);

    /// <summary>Converts a number.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(double value) => FromDouble(value);

    /// <summary>Converts a number; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(double? value) => FromDouble(value);

    /// <summary>Converts a number by its shortest decimal form (<c>0.1f</c> is written <c>0.1</c>).</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(float value) =>
        FromDouble(float.IsFinite(value) ? double.Parse(value.ToString("R", CultureInfo.InvariantCulture), CultureInfo.InvariantCulture) : value);

    /// <summary>Converts a number by its decimal digits.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(decimal value) =>
        FromDouble(double.Parse(value.ToString(CultureInfo.InvariantCulture), CultureInfo.InvariantCulture));

    /// <summary>Converts a boolean.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(bool value) => FromBoolean(value);

    /// <summary>Converts a boolean; null is the empty argument.</summary>
    /// <param name="value">The value.</param>
    public static implicit operator OptionArg(bool? value) => FromBoolean(value);

    /// <summary>Formats the argument for an option segment.</summary>
    /// <param name="separator">The argument separator, which a string may not contain.</param>
    /// <param name="where">Where the argument is, for error messages.</param>
    /// <returns>The argument as written in the URL.</returns>
    internal string FormatOption(string separator, string where) => kind switch
    {
        ArgKind.Null => string.Empty,
        ArgKind.Boolean => integer == 1 ? "1" : "0",
        ArgKind.Integer => integer.ToString(CultureInfo.InvariantCulture),
        ArgKind.Number => Text.FormatNumber(number, where),
        _ => Text.CheckOptionArgument(text!, separator, where),
    };

    /// <summary>Formats the argument as a native query value.</summary>
    /// <param name="where">Where the value is, for error messages.</param>
    /// <returns>The value, not yet escaped.</returns>
    internal string FormatQuery(string where) => kind switch
    {
        ArgKind.Null => string.Empty,
        ArgKind.Boolean => integer == 1 ? "true" : "false",
        ArgKind.Integer => integer.ToString(CultureInfo.InvariantCulture),
        ArgKind.Number => Text.FormatNumber(number, where),
        _ => text!,
    };

    /// <summary>The argument as written in an option with the default separator, or <c>&lt;invalid&gt;</c>.</summary>
    /// <returns>The text.</returns>
    public override string ToString()
    {
        try
        {
            return FormatOption(XtandardSpec.DefaultSeparator, "argument");
        }
        catch (UrlBuildException)
        {
            return "<invalid>";
        }
    }
}

/// <summary>Helpers of the generated per-option methods and records.</summary>
internal static class OptionArgs
{
    /// <summary>Drops trailing empty arguments past the required ones (optional parameters left out).</summary>
    internal static OptionArg[] Trim(OptionArg[] args, int required)
    {
        var n = args.Length;
        while (n > required && args[n - 1].IsNull)
        {
            n--;
        }

        return n == args.Length ? args : args[..n];
    }
}
