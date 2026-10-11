using System;
using System.Globalization;

namespace DobbyVPN.Windows;

internal static class ProfileAutomationIds
{
    internal static string Create(string digest, int index, string role)
    {
        if (string.IsNullOrWhiteSpace(digest) || digest.Contains('.'))
            throw new ArgumentException("Profile automation identifiers require one nonempty digest", nameof(digest));
        if (index < 0) throw new ArgumentOutOfRangeException(nameof(index));
        if (role is not ("description" or "protocol" or "action"))
            throw new ArgumentException("Unknown profile automation role", nameof(role));
        return $"profile.{digest}.{index.ToString(CultureInfo.InvariantCulture)}.{role}";
    }

    internal static bool TryParse(string value, out string digest, out int index, out string role)
    {
        digest = "";
        index = -1;
        role = "";
        var parts = value.Split('.', 4, StringSplitOptions.None);
        if (parts.Length != 4 || parts[0] != "profile" || string.IsNullOrWhiteSpace(parts[1]) ||
            !int.TryParse(parts[2], NumberStyles.None, CultureInfo.InvariantCulture, out index) || index < 0 ||
            parts[3] is not ("description" or "protocol" or "action"))
            return false;
        digest = parts[1];
        role = parts[3];
        return true;
    }
}
