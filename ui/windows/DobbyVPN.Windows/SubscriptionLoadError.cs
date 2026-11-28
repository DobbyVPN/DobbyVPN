using System;

namespace DobbyVPN.Windows;

/// <summary>Builds the user-facing message for a failed subscription load without printing the subscription link, its host, or its path and token. The error dialog's Copy action copies this exact string.</summary>
internal static class SubscriptionLoadError
{
    internal static string Compose(string source, string accepted, bool configured, bool sameSource, string detail)
    {
        var owner = configured && !sameSource && accepted.Length > 0
            ? " Showing profiles from the previously accepted subscription." : "";
        return $"Could not load the subscription link: {Scrub(detail, source, accepted)}.{owner}";
    }

    private static string Scrub(string text, string source, string accepted)
    {
        var scrubbed = text;
        foreach (var secret in new[] { source, accepted })
        {
            if (string.IsNullOrEmpty(secret)) continue;
            scrubbed = scrubbed.Replace(secret, "the subscription link", StringComparison.OrdinalIgnoreCase);
        }
        foreach (var secret in new[] { source, accepted })
        {
            if (string.IsNullOrEmpty(secret)) continue;
            var host = HostOf(secret);
            if (string.IsNullOrEmpty(host)) continue;
            scrubbed = scrubbed.Replace(host, "the subscription server", StringComparison.OrdinalIgnoreCase);
        }
        return scrubbed;
    }

    private static string HostOf(string value) =>
        Uri.TryCreate(value, UriKind.Absolute, out var uri) ? uri.Host : "";
}
