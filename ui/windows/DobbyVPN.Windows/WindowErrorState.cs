namespace DobbyVPN.Windows;

internal enum SnapshotFailure
{
    ServiceUnavailable,
    ProtocolMismatch,
}

/// <summary>Keeps service availability separate from errors in user operations.</summary>
internal sealed class WindowErrorState
{
    private SnapshotFailure? _snapshotFailure;

    internal string Operation { get; set; } = "";
    internal string Snapshot => _snapshotFailure switch
    {
        SnapshotFailure.ServiceUnavailable => "VPN service is unavailable. See logs for details.",
        SnapshotFailure.ProtocolMismatch => SessionProtocol.RestartMessage,
        _ => "",
    };
    internal bool SnapshotFailed => _snapshotFailure.HasValue;

    internal bool FailSnapshot(SnapshotFailure failure)
    {
        if (_snapshotFailure == failure) return false;
        _snapshotFailure = failure;
        return true;
    }

    internal bool RecoverSnapshot()
    {
        var recovering = SnapshotFailed;
        _snapshotFailure = null;
        return recovering;
    }
}
