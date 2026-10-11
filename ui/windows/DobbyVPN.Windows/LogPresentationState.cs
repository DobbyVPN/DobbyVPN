using System;
using System.Collections.Generic;
using System.Linq;

namespace DobbyVPN.Windows;

// UI-independent state consumed directly by MainWindow while it renders logs.
internal sealed class LogPresentationState
{
    private List<NativeDiagnostics.Entry> _latestEntries = [];
    private List<NativeDiagnostics.Entry> _visibleEntries = [];
    private readonly HashSet<string> _expandedEntries = [];
    // This set mirrors the current bounded diagnostics preview; it is not a
    // history of every backend record the app has ever read.
    private HashSet<string> _latestBackendEntryIds = new(StringComparer.Ordinal);
    private bool _hasSeededBackendEntryIds;

    internal readonly record struct BackendEntryAcknowledgement(string EntryId, bool FollowsTail);

    public IReadOnlyList<NativeDiagnostics.Entry> LatestEntries => _latestEntries;
    public IReadOnlyList<NativeDiagnostics.Entry> VisibleEntries => _visibleEntries;
    public bool FollowsTail { get; private set; } = true;

    public IReadOnlyList<BackendEntryAcknowledgement> ReplaceEntries(IEnumerable<NativeDiagnostics.Entry> entries)
    {
        var latestEntries = entries.ToList();
        var latestBackendEntryIds = latestEntries
            .Where(IsBackendEntry)
            .Select(entry => entry.Id)
            .ToHashSet(StringComparer.Ordinal);
        var acknowledgements = _hasSeededBackendEntryIds
            ? latestEntries
                .Where(entry => IsBackendEntry(entry) && !_latestBackendEntryIds.Contains(entry.Id))
                .Select(entry => new BackendEntryAcknowledgement(entry.Id, FollowsTail))
                .ToArray()
            : Array.Empty<BackendEntryAcknowledgement>();

        _latestEntries = latestEntries;
        _latestBackendEntryIds = latestBackendEntryIds;
        _hasSeededBackendEntryIds = true;
        if (FollowsTail) ShowLatestEntries();
        return acknowledgements;
    }

    public void Freeze() => FollowsTail = false;

    public void FollowTail()
    {
        FollowsTail = true;
        ShowLatestEntries();
    }

    public void ObserveUserScroll(bool isAtBottom)
    {
        if (isAtBottom) FollowTail();
        else Freeze();
    }

    public bool IsExpanded(string entryId) => _expandedEntries.Contains(entryId);

    public void SetExpanded(string entryId, bool expanded)
    {
        if (expanded) _expandedEntries.Add(entryId);
        else _expandedEntries.Remove(entryId);
    }

    public void ClearAfterBoundary()
    {
        _latestEntries = [];
        _visibleEntries = [];
        _latestBackendEntryIds.Clear();
        _expandedEntries.Clear();
        FollowTail();
    }

    private static bool IsBackendEntry(NativeDiagnostics.Entry entry) =>
        entry.Source == "Backend" || entry.Source == "Backend stderr" ||
        entry.Source.StartsWith("Backend · ", StringComparison.Ordinal) ||
        entry.Source.StartsWith("Backend stderr · ", StringComparison.Ordinal);

    private void ShowLatestEntries()
    {
        _visibleEntries = _latestEntries.ToList();
        _expandedEntries.IntersectWith(_visibleEntries.Select(entry => entry.Id));
    }

    public static string DetailsAutomationId(NativeDiagnostics.Entry entry) =>
        $"Log details {entry.Id}";

    // Keep the existing accessible label; identity is carried only by the stable ID.
    public static string DetailsAccessibleName(NativeDiagnostics.Entry entry) => "Details";
}
