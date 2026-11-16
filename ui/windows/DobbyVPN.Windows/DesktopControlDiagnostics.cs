using System.Diagnostics;
using System.Text.Json;

namespace DobbyVPN.Windows;

// Correlates one native control exchange without retaining configuration data.
internal sealed class DesktopControlDiagnostics(Func<TimeSpan>? clock = null)
{
    private readonly object _gate = new();
    private readonly Func<TimeSpan> _clock = clock ?? (() => Stopwatch.GetElapsedTime(0, Stopwatch.GetTimestamp()));
    private TimeSpan? _lastSnapshot;

    internal sealed record Request(string Id, string Method, bool Sampled, object Parameters, IReadOnlyDictionary<string, object?> Identity)
    {
        internal string Encode() => JsonSerializer.Serialize(new { method = Method, @params = Parameters, request_id = Id, diagnostic_sampled = Sampled });
    }

    internal Request Begin(string method, object parameters)
    {
        var sampled = method != "Snapshot";
        if (!sampled)
        {
            lock (_gate)
            {
                var now = _clock();
                if (_lastSnapshot is null || now - _lastSnapshot.Value >= TimeSpan.FromSeconds(30) || now < _lastSnapshot.Value)
                {
                    _lastSnapshot = now;
                    sampled = true;
                }
            }
        }
        var identity = new Dictionary<string, object?>();
        foreach (var name in new[] { "session_id", "generation", "expected_sequence" })
        {
            var property = parameters.GetType().GetProperty(name);
            if (property is not null)
            {
                var value = property.GetValue(parameters);
                if (value is string or int or long or uint or ulong) identity[name] = value;
            }
        }
        return new Request(Guid.NewGuid().ToString("N"), method, sampled, parameters, identity);
    }
}
