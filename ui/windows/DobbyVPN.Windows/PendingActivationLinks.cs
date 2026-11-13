using System.Globalization;

namespace DobbyVPN.Windows;

/// <summary>
/// Keeps <c>dobbyvpn://</c> links that no running window accepted (the running
/// instance did not answer a redirect in time, or was already closing) so the
/// next instance that starts can import them instead of dropping them silently.
/// Every save publishes its own file (written under a temporary name, then renamed), so concurrent processes never share a file handle; each file holds lines of <c>unix-seconds TAB escaped-link</c>.
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
    private const string Prefix = "pending-link-";
    private const string Extension = ".txt";

    private readonly string _directory;
    private readonly Func<DateTimeOffset> _now;

    internal PendingActivationLinks(string directory, Func<DateTimeOffset>? now = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(directory);
        _directory = directory;
        _now = now ?? (() => DateTimeOffset.UtcNow);
    }

    internal static PendingActivationLinks Default { get; } = new(Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DobbyVPN", "pending-links"));

    internal void Save(IEnumerable<string> links)
    {
        ArgumentNullException.ThrowIfNull(links);
        var now = _now();
        var stamp = now.ToUnixTimeSeconds().ToString(CultureInfo.InvariantCulture);
        var lines = links.Select(link => stamp + "\t" + Uri.EscapeDataString(link)).ToArray();
        if (lines.Length == 0) return;
        Directory.CreateDirectory(_directory);
        // Names sort by save time; the GUID keeps simultaneous saves apart.
        var name = Prefix + now.UtcTicks.ToString("D19", CultureInfo.InvariantCulture) + "-" + Guid.NewGuid().ToString("N");
        var temporary = Path.Combine(_directory, name + ".tmp");
        File.WriteAllLines(temporary, lines);
        File.Move(temporary, Path.Combine(_directory, name + Extension));
    }

    /// <summary>
    /// Removes every saved link and returns the ones that are still fresh, in
    /// save order. Each file is renamed to a name only this call uses before it
    /// is read, so two concurrent takers never read the same links; a file that
    /// cannot be renamed (another process got it first) is skipped.
    /// </summary>
    internal IReadOnlyList<string> TakeFresh()
    {
        if (!Directory.Exists(_directory)) return [];
        var oldest = _now() - MaxAge;
        var fresh = new List<string>();
        var files = Directory.GetFiles(_directory, Prefix + "*" + Extension);
        Array.Sort(files, StringComparer.Ordinal);
        foreach (var file in files)
        {
            var taken = file + "." + Environment.ProcessId.ToString(CultureInfo.InvariantCulture) + ".taken";
            try { File.Move(file, taken); }
            catch (FileNotFoundException) { continue; }
            catch (IOException) { continue; }
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
        }
        return fresh;
    }
}
