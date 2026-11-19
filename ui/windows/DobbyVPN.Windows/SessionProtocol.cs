namespace DobbyVPN.Windows;

// Version 1 includes ParseImportLink and the session lifecycle commands.
internal static class SessionProtocol
{
    internal const int ExpectedVersion = 1;
    internal const string RestartMessage = "The app and the VPN service are different versions. Restart the app.";

    internal static void RequireCompatible(int version)
    {
        if (version != ExpectedVersion) throw new SessionProtocolMismatchException();
    }
}

internal sealed class SessionProtocolMismatchException : Exception
{
    internal SessionProtocolMismatchException() : base(SessionProtocol.RestartMessage) { }
}
