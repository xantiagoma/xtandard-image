using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;
using Xtandard.Image;

namespace Microsoft.Extensions.DependencyInjection;

/// <summary>Registers xtandard-image in a service collection (ASP.NET Core, the Generic Host).</summary>
public static class XtandardImageServiceCollectionExtensions
{
    /// <summary>
    /// Registers <see cref="XtandardClientOptions"/>, the typed <see cref="XtandardClient"/> through
    /// <c>IHttpClientFactory</c>, and <see cref="ImgproxyUrlBuilder"/> and <see cref="QueryUrlBuilder"/> as
    /// singletons built from the same options.
    /// </summary>
    /// <remarks>
    /// <code>
    /// builder.Services.AddXtandardImage(o =>
    /// {
    ///     o.BaseUrl = "https://img.example.com";
    ///     o.Signing = new SigningKey(builder.Configuration["Xi:Key"]!, builder.Configuration["Xi:Salt"]!);
    /// }).AddStandardResilienceHandler(); // optional, Microsoft.Extensions.Http.Resilience
    /// </code>
    /// The returned builder configures the HTTP client (handlers, resilience, the primary handler).
    /// <see cref="XtandardClientOptions.Timeout"/> applies only to clients that create their own
    /// <see cref="HttpClient"/>; configure the timeout here with <c>ConfigureHttpClient</c>.
    /// </remarks>
    /// <param name="services">The service collection.</param>
    /// <param name="configure">Sets the options.</param>
    /// <returns>The HTTP client builder of <see cref="XtandardClient"/>.</returns>
    public static IHttpClientBuilder AddXtandardImage(this IServiceCollection services, Action<XtandardClientOptions> configure)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(configure);
        services.AddOptions<XtandardClientOptions>().Configure(configure);
        services.TryAddSingleton(sp => new ImgproxyUrlBuilder(sp.GetRequiredService<IOptions<XtandardClientOptions>>().Value.ToImgproxyConfig()));
        services.TryAddSingleton(sp => new QueryUrlBuilder(sp.GetRequiredService<IOptions<XtandardClientOptions>>().Value.ToQueryConfig()));
        return services
            .AddHttpClient(nameof(XtandardClient))
            .AddTypedClient((http, sp) => new XtandardClient(http, sp.GetRequiredService<IOptions<XtandardClientOptions>>().Value));
    }
}
