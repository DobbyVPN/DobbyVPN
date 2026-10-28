using System;
using System.Collections.Generic;
using System.Linq;

namespace DobbyVPN.Windows;

// UI-independent state consumed directly by MainWindow while it renders logs.
internal sealed class LogPresentationState
{
    private List<NativeDiagnostics.Entry> _latestEntries = [];
    private List<NativeDiagnostics.Entry> _visibleEntries = [];
    // This set mirrors the current bounded diagnostics preview; it is not a
    // history of every backend record the app has ever read.
    private HashSet<string> _latestBackendEntryIds = new(StringComparer.Ordinal);
    private bool _hasSeededBackendEntryIds;
    private (long Generation, double Height)? _settledTailAlignment;

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

    public void Freeze()
    {
        FollowsTail = false;
        _settledTailAlignment = null;
    }

    public bool TryRequestSettledTailAlignment(bool isIntermediate, double verticalOffset,
        double scrollableHeight, long renderGeneration)
    {
        if (isIntermediate || !FollowsTail || verticalOffset == scrollableHeight) return false;
        if (_settledTailAlignment is { } previous && previous.Generation == renderGeneration &&
            previous.Height == scrollableHeight) return false;
        // One correction per layout target prevents an unrepresentable offset
        // from repeatedly scheduling another callback. A new reading gesture,
        // render, or viewport height permits a new correction.
        _settledTailAlignment = (renderGeneration, scrollableHeight);
        return true;
    }

    public void FollowTail()
    {
        FollowsTail = true;
        ShowLatestEntries();
    }

    public void ObserveUserScroll(bool isAtBottom, bool movedTowardEarlierEntries)
    {
        if (isAtBottom) FollowTail();
        else if (movedTowardEarlierEntries) Freeze();
    }

    public void ObserveDirectedDownScroll(bool isAtBottom)
    {
        // Resuming can append rows before the downward animation finishes.
        // Its later positions must not freeze following against the larger layout.
        if (!FollowsTail && isAtBottom) FollowTail();
    }

    public void ClearAfterBoundary()
    {
        _settledTailAlignment = null;
        _latestEntries = [];
        _visibleEntries = [];
        _latestBackendEntryIds.Clear();
        FollowTail();
    }

    private static bool IsBackendEntry(NativeDiagnostics.Entry entry) =>
        entry.Source == "Backend" || entry.Source == "Backend stderr" ||
        entry.Source.StartsWith("Backend · ", StringComparison.Ordinal) ||
        entry.Source.StartsWith("Backend stderr · ", StringComparison.Ordinal);

    private void ShowLatestEntries()
    {
        _visibleEntries = _latestEntries.ToList();
    }
}
