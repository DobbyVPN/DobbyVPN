using System;

namespace DobbyVPN.Windows;

internal sealed class ProtocolMismatchException(int expected, int received)
    : Exception($"Expected VPN service protocol version {expected}, received {received}.")
{
    internal const string RestartMessage = "The app and the VPN service are different versions. Restart the app.";
}

internal enum SnapshotFailure
{
    ServiceUnavailable,
    ProtocolMismatch,
}

/// <summary>Keeps service availability separate from errors in user operations.</summary>
internal sealed class WindowErrorState
{
    private const int ExpectedProtocolVersion = 1;

    internal static void RequireCompatibleProtocol(int received)
    {
        if (received != ExpectedProtocolVersion)
            throw new ProtocolMismatchException(ExpectedProtocolVersion, received);
    }

    private SnapshotFailure? snapshotFailure;

    internal string Operation { get; set; } = "";
    internal string Snapshot => snapshotFailure switch
    {
        SnapshotFailure.ServiceUnavailable => "VPN service is unavailable. See logs for details.",
        SnapshotFailure.ProtocolMismatch => ProtocolMismatchException.RestartMessage,
        _ => "",
    };
    internal bool SnapshotFailed => snapshotFailure.HasValue;

    internal bool FailSnapshot(SnapshotFailure failure)
    {
        if (snapshotFailure == failure) return false;
        snapshotFailure = failure;
        return true;
    }

    internal bool RecoverSnapshot()
    {
        var recovering = SnapshotFailed;
        snapshotFailure = null;
        return recovering;
    }
}
