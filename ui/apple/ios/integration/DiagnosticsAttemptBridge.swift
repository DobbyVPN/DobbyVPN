import Foundation

// The operation identity a transport type needs, without that type depending on
// the diagnostic writer module. The app installs its own implementation at
// startup; until then the value is one fresh per-process identity, so a command
// always carries something correlatable.
public enum DiagnosticsAttemptBridge {
    public static var provider: () -> String = { fallback }

    public static var current: String { provider() }

    private static let fallback: String =
        UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
}
