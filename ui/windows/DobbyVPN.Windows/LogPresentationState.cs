using System.Collections.Generic;
using System.Linq;

namespace DobbyVPN.Windows;

// UI-independent state consumed directly by MainWindow while it renders logs.
internal sealed class LogPresentationState
{
    private List<NativeDiagnostics.Entry> _latestEntries = [];
    private List<NativeDiagnostics.Entry> _visibleEntries = [];
    private readonly HashSet<string> _expandedEntries = [];

    public IReadOnlyList<NativeDiagnostics.Entry> LatestEntries => _latestEntries;
    public IReadOnlyList<NativeDiagnostics.Entry> VisibleEntries => _visibleEntries;
    public bool FollowsTail { get; private set; } = true;

    public void ReplaceEntries(IEnumerable<NativeDiagnostics.Entry> entries)
    {
        _latestEntries = entries.ToList();
        if (FollowsTail) ShowLatestEntries();
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
        _expandedEntries.Clear();
        FollowTail();
    }

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
