using System;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Threading;
using System.Threading.Tasks;

namespace DobbyVPN.Windows;

// The backend supplies the destination and authorization from the accepted
// configuration. Never follow a redirect with those credentials.
internal static class LogUpload
{
    internal static async Task PutAsync(string path, string destination, string authorization, long maxBytes,
        CancellationToken cancellationToken, HttpMessageHandler? handler = null)
        => await PutCoreAsync(path, destination, authorization, maxBytes, cancellationToken, TimeSpan.FromMinutes(15), handler);

    // Allows deterministic deadline checks without changing the production deadline.
    internal static Task PutWithTimeoutForTestAsync(string path, string destination, string authorization, long maxBytes,
        CancellationToken cancellationToken, TimeSpan timeout, HttpMessageHandler? handler = null)
        => PutCoreAsync(path, destination, authorization, maxBytes, cancellationToken, timeout, handler);

    private static async Task PutCoreAsync(string path, string destination, string authorization, long maxBytes,
        CancellationToken cancellationToken, TimeSpan duration, HttpMessageHandler? handler)
    {
        if (!Uri.TryCreate(destination, UriKind.Absolute, out var uri) || uri.Scheme != Uri.UriSchemeHttps || !string.IsNullOrEmpty(uri.UserInfo))
            throw new InvalidDataException("The diagnostic upload destination must be an HTTPS URL without embedded credentials.");
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(duration);
        using var client = handler is null
            ? new HttpClient(new HttpClientHandler { AllowAutoRedirect = false })
            : new HttpClient(handler, false);
        client.Timeout = Timeout.InfiniteTimeSpan;
        await using var input = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 65536, true);
        if (input.Length > maxBytes) throw new InvalidDataException("Compressed diagnostics exceed the upload limit.");
        using var request = new HttpRequestMessage(HttpMethod.Put, destination);
        request.Headers.TryAddWithoutValidation("If-None-Match", "*");
        if (!string.IsNullOrEmpty(authorization)) request.Headers.TryAddWithoutValidation("Authorization", authorization);
        request.Content = new StreamContent(input);
        request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/gzip");
        request.Content.Headers.ContentLength = input.Length;
        using var response = await client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, timeout.Token);
        if (response.StatusCode != HttpStatusCode.Created)
            throw new HttpRequestException($"Diagnostic upload failed with HTTP {(int)response.StatusCode}.");
    }
}
