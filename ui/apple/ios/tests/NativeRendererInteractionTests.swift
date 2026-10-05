import XCTest
import UIKit

final class NativeRendererInteractionTests: XCTestCase {
    func testSeverityColorsResolveForLightAndDarkAppearances() throws {
        let entries = [
            entry("debug", level: "DEBUG", message: "muted trace"),
            entry("info", level: "INFO", message: "normal information"),
            entry("warn", level: "WARN", message: "amber warning"),
            entry("error", level: "ERROR", message: "red failure"),
        ]
        let rendered = logText(entries, expanded: [])
        let cases: [(String, String, UIColor)] = [
            ("muted trace", "DEBUG", .secondaryLabel),
            ("normal information", "INFO", .label),
            ("amber warning", "WARN", .systemOrange),
            ("red failure", "ERROR", .systemRed),
        ]

        for (appearance, style) in [("light", UIUserInterfaceStyle.light), ("dark", .dark)] {
            let traits = UITraitCollection(userInterfaceStyle: style)
            for (message, severity, expectedColor) in cases {
                let renderedColor = try XCTUnwrap(
                    color(of: message, in: rendered),
                    "Missing \(severity) color for \(message) in \(appearance) appearance"
                )
                let actual = renderedColor.resolvedColor(with: traits)
                let expected = expectedColor.resolvedColor(with: traits)
                XCTAssertTrue(
                    actual.isEqual(expected),
                    "\(severity) should use its palette color in \(appearance) appearance"
                )
            }
        }
    }

    private func entry(_ id: String, level: String, message: String) -> DobbyLogEntry {
        DobbyLogEntry(
            id: id,
            timestamp: "2026-10-05T10:00:00Z",
            level: level,
            source: "native-ui-test",
            message: message,
            raw: "{\"level\":\"\(level)\",\"message\":\"\(message)\"}"
        )
    }

    private func color(of message: String, in rendered: NSAttributedString) -> UIColor? {
        let range = (rendered.string as NSString).range(of: message)
        guard range.location != NSNotFound else { return nil }
        return rendered.attribute(.foregroundColor, at: range.location, effectiveRange: nil) as? UIColor
    }
}
