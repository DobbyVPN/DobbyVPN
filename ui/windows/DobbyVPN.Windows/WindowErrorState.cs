using System;

namespace DobbyVPN.Windows;

internal sealed class ProtocolMismatchException(int expected, int received)
    : Exception($"Expected VPN service protocol version {expected}, received {received}.")
{
    internal const string RestartMessage = "The app and the VPN service are different versions. Restart the app.";
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

    internal string Operation { get; set; } = "";
    internal string Snapshot { get; private set; } = "";
    internal bool SnapshotFailed => Snapshot.Length > 0;

    internal bool FailSnapshot(string message)
    {
        if (Snapshot == message) return false;
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
