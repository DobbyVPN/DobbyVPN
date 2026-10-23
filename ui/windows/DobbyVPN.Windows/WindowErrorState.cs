namespace DobbyVPN.Windows;

/// <summary>Keeps service availability separate from errors in user operations.</summary>
internal sealed class WindowErrorState
{
    internal string Operation { get; set; } = "";
    internal string Snapshot { get; private set; } = "";
    internal bool SnapshotFailed => Snapshot.Length > 0;

    internal bool FailSnapshot(string message)
    {
        if (SnapshotFailed) return false;
        Snapshot = message;
        return true;
    }

    internal bool RecoverSnapshot()
    {
        var recovering = SnapshotFailed;
        Snapshot = "";
        return recovering;
    }
}
