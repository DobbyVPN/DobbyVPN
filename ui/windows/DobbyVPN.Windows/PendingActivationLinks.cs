using System.Globalization;

namespace DobbyVPN.Windows;

/// <summary>
/// Keeps <c>dobbyvpn://</c> links that no running window accepted (the running
/// instance did not answer a redirect in time, or was already closing) so the
/// next instance that starts can import them instead of dropping them silently.
/// Links are stored one per line as <c>unix-seconds TAB escaped-link</c>.
/// </summary>
internal sealed class PendingActivationLinks
{
    /// <summary>
    /// A saved link is replayed only if it is at most this old. A redirect that
    /// timed out may still have reached a slow first instance, so a link must
    /// not resurface long afterwards and replace a subscription the user has
    /// since chosen; ten minutes covers the user restarting a stuck app.
    /// </summary>
    internal static readonly TimeSpan MaxAge = TimeSpan.FromMinutes(10);

    private const long MaxUnixSeconds = 253402300799; // 9999-12-31T23:59:59Z

    private readonly string _path;
    private readonly Func<DateTimeOffset> _now;

    internal PendingActivationLinks(string path, Func<DateTimeOffset>? now = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        _path = path;
        _now = now ?? (() => DateTimeOffset.UtcNow);
    }

    internal static PendingActivationLinks Default { get; } = new(Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DobbyVPN", "pending-links.txt"));

    internal void Save(IEnumerable<string> links)
    {
        ArgumentNullException.ThrowIfNull(links);
        var stamp = _now().ToUnixTimeSeconds().ToString(CultureInfo.InvariantCulture);
        var lines = links.Select(link => stamp + "\t" + Uri.EscapeDataString(link)).ToArray();
        if (lines.Length == 0) return;
        Directory.CreateDirectory(Path.GetDirectoryName(_path)!);
        File.AppendAllLines(_path, lines);
    }

    /// <summary>
    /// Removes every saved link and returns the ones that are still fresh, in
    /// the order they were saved. The file is renamed first so a concurrent
    /// <see cref="Save"/> from another process lands in a new file instead of
    /// being deleted with this one.
    /// </summary>
    internal IReadOnlyList<string> TakeFresh()
    {
        if (!File.Exists(_path)) return [];
        var taken = _path + "." + Environment.ProcessId.ToString(CultureInfo.InvariantCulture) + ".taken";
        try { File.Move(_path, taken, overwrite: true); }
        catch (FileNotFoundException) { return []; }
        catch (DirectoryNotFoundException) { return []; }

        var oldest = _now() - MaxAge;
        var fresh = new List<string>();
        try
        {
            foreach (var line in File.ReadLines(taken))
            {
                var separator = line.IndexOf('\t');
                if (separator <= 0 ||
                    !long.TryParse(line.AsSpan(0, separator), NumberStyles.None, CultureInfo.InvariantCulture, out var seconds))
                    continue;
                if (seconds > MaxUnixSeconds || DateTimeOffset.FromUnixTimeSeconds(seconds) < oldest) continue;
                fresh.Add(Uri.UnescapeDataString(line[(separator + 1)..]));
            }
        }
        finally { File.Delete(taken); }
        return fresh;
    }
}
