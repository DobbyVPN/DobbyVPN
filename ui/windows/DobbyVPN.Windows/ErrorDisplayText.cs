using System.Text;

namespace DobbyVPN.Windows;

internal static class ErrorDisplayText
{
    internal const int Limit = 32768;
    internal const string TruncationNotice = "\n\n[Error details truncated. Copy for the full original.]";

    internal static string ForDisplay(string original)
    {
        var count = 0;
        var length = 0;
        foreach (var rune in original.EnumerateRunes())
        {
            if (count++ == Limit) return original[..length] + TruncationNotice;
            length += rune.Utf16SequenceLength;
        }
        return original;
    }
}
