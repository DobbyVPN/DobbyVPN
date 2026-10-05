#if os(macOS)
import AppKit
import SwiftUI
@testable import DobbyDiagnosticFiles
@testable import DobbyNativeUI
import XCTest

final class LogRenderingTests: XCTestCase {
    func testMacLogRenderingPreservesFieldsRawDetailsAndSeverityColors() throws {
        let rawError = #"{"timestamp":"2026-10-04T10:00:04Z","level":"ERROR","source":"transport","message":"failed","detail":"socket closed"}"#
        let rawCapture = #"{"timestamp":"2026-10-04T10:00:00Z","event":"stderr.capture","level":"ERROR","source":"tunnel","message":"capture initialized"}"#
        let entries = [
            entry(id: "capture", timestamp: "10:00:00", level: "INFO", source: "Tunnel stderr · tunnel", message: "Stderr capture initialized", raw: rawCapture),
            entry(id: "debug", timestamp: "10:00:01", level: "DEBUG", source: "Tunnel", message: "trace line", raw: #"{"detail":"trace"}"#),
            entry(id: "info", timestamp: "10:00:02", level: "INFO", source: "Backend", message: "ready", raw: #"{"detail":"ready"}"#),
            entry(id: "warn", timestamp: "10:00:03", level: "WARN", source: "Backend", message: "slow", raw: #"{"detail":"slow"}"#),
            entry(id: "error", timestamp: "10:00:04", level: "ERROR", source: "Backend · transport", message: "failed", raw: rawError),
        ]

        let rendered = logText(entries, expanded: ["capture", "error"])
        XCTAssertTrue(rendered.string.contains("10:00:00 · INFO · Tunnel stderr · tunnel\nStderr capture initialized\nHide details\n"))
        XCTAssertTrue(rendered.string.contains(rawCapture), "The capture Details action must preserve its original record")
        XCTAssertTrue(rendered.string.contains("10:00:01 · DEBUG · Tunnel\ntrace line\nDetails\n"))
        XCTAssertTrue(rendered.string.contains("10:00:02 · INFO · Backend\nready\nDetails\n"))
        XCTAssertTrue(rendered.string.contains("10:00:03 · WARN · Backend\nslow\nDetails\n"))
        XCTAssertTrue(rendered.string.contains("10:00:04 · ERROR · Backend · transport\nfailed\nHide details\n"))
        XCTAssertTrue(rendered.string.contains(rawError), "Expanded details must include the exact original record")
        let traceColor = try XCTUnwrap(color(of: "trace line", in: rendered))
        let infoColor = try XCTUnwrap(color(of: "ready", in: rendered))
        let warningColor = try XCTUnwrap(color(of: "slow", in: rendered))
        let errorColor = try XCTUnwrap(color(of: "failed", in: rendered))
        XCTAssertTrue(traceColor.isEqual(NSColor.secondaryLabelColor))
        XCTAssertTrue(infoColor.isEqual(NSColor.textColor))
        XCTAssertTrue(warningColor.isEqual(NSColor.systemOrange))
        XCTAssertTrue(errorColor.isEqual(NSColor.systemRed))

        let light = try XCTUnwrap(NSAppearance(named: .aqua))
        let dark = try XCTUnwrap(NSAppearance(named: .darkAqua))
        let lightText = try XCTUnwrap(resolved(NSColor.textColor, in: light))
        let darkText = try XCTUnwrap(resolved(NSColor.textColor, in: dark))
        let lightMuted = try XCTUnwrap(resolved(NSColor.secondaryLabelColor, in: light))
        let darkMuted = try XCTUnwrap(resolved(NSColor.secondaryLabelColor, in: dark))
        let lightTrace = try XCTUnwrap(resolved(traceColor, in: light))
        let darkTrace = try XCTUnwrap(resolved(traceColor, in: dark))
        let lightInfo = try XCTUnwrap(resolved(infoColor, in: light))
        let darkInfo = try XCTUnwrap(resolved(infoColor, in: dark))
        let lightWarning = try XCTUnwrap(resolved(warningColor, in: light))
        let darkWarning = try XCTUnwrap(resolved(warningColor, in: dark))
        let lightError = try XCTUnwrap(resolved(errorColor, in: light))
        let darkError = try XCTUnwrap(resolved(errorColor, in: dark))
        let lightOrange = try XCTUnwrap(resolved(NSColor.systemOrange, in: light))
        let darkOrange = try XCTUnwrap(resolved(NSColor.systemOrange, in: dark))
        let lightRed = try XCTUnwrap(resolved(NSColor.systemRed, in: light))
        let darkRed = try XCTUnwrap(resolved(NSColor.systemRed, in: dark))
        XCTAssertTrue(lightTrace.isEqual(lightMuted))
        XCTAssertTrue(darkTrace.isEqual(darkMuted))
        XCTAssertTrue(lightInfo.isEqual(lightText))
        XCTAssertTrue(darkInfo.isEqual(darkText))
        XCTAssertTrue(lightWarning.isEqual(lightOrange))
        XCTAssertTrue(darkWarning.isEqual(darkOrange))
        XCTAssertTrue(lightError.isEqual(lightRed))
        XCTAssertTrue(darkError.isEqual(darkRed))
        XCTAssertFalse(lightText.isEqual(darkText))
        XCTAssertFalse(lightMuted.isEqual(darkMuted))
    }

    @MainActor
    func testMacLogTextViewAllowsSelectionWithoutEditing() {
        let view = NSTextView(frame: .zero)
        configureLogTextView(view)
        view.string = "selected diagnostic text"
        XCTAssertFalse(view.isEditable)
        XCTAssertTrue(view.isSelectable)
        view.setSelectedRange(NSRange(location: 0, length: 8))
        XCTAssertEqual((view.string as NSString).substring(with: view.selectedRange()), "selected")
    }

    @MainActor
    func testMacLogDetailsInteractionExpandsAndCollapsesOriginalRecord() {
        let raw = #"{"schema":"dobby.log/v1","timestamp":"2026-10-04T10:00:04Z","level":"ERROR","source":"transport","message":"failed","detail":"socket closed"}"#
        let entry = entry(id: "error", timestamp: "10:00:04", level: "ERROR", source: "Backend · transport", message: "failed", raw: raw)
        let logView = DobbyLogView(entries: [entry], following: .constant(true), clear: 0)
        let coordinator = logView.makeCoordinator()
        coordinator.entries = [entry]
        let textView = NSTextView(frame: .zero)
        configureLogTextView(textView)

        XCTAssertTrue(coordinator.textView(textView, clickedOnLink: URL(string: "dobbylog://record/0")!, at: 0))
        XCTAssertTrue(textView.string.contains(raw), "Details should reveal the complete source record")
        XCTAssertTrue(coordinator.expanded.contains("error"))
        XCTAssertTrue(coordinator.textView(textView, clickedOnLink: URL(string: "dobbylog://record/0")!, at: 0))
        XCTAssertFalse(textView.string.contains(raw), "Hide details should collapse the source record")
        XCTAssertFalse(coordinator.expanded.contains("error"))
    }

    @MainActor
    func testClearResetsFollowAndCollapsesExpandedRecords() {
        var following = false
        var expanded: Set<String> = ["record"]
        let binding = Binding(get: { following }, set: { following = $0 })
        resetLogPresentationForClear(following: binding, expanded: &expanded)
        XCTAssertTrue(following)
        XCTAssertTrue(expanded.isEmpty)
    }

    func testScrollPositionFreezesAboveBottomAndResumesWithinFollowThreshold() {
        XCTAssertFalse(shouldFollowLogUpdates(viewportBottom: 500, contentHeight: 1_000))
        XCTAssertTrue(shouldFollowLogUpdates(viewportBottom: 976, contentHeight: 1_000))
        XCTAssertTrue(shouldFollowLogUpdates(viewportBottom: 100, contentHeight: 110))
    }

    private func entry(id: String, timestamp: String, level: String, source: String, message: String, raw: String) -> DiagnosticFiles.Entry {
        DiagnosticFiles.Entry(id: id, timestamp: timestamp, level: level, source: source, message: message, raw: raw, date: nil)
    }

    private func color(of text: String, in rendered: NSAttributedString) -> NSColor? {
        let range = (rendered.string as NSString).range(of: text)
        guard range.location != NSNotFound else { return nil }
        return rendered.attribute(.foregroundColor, at: range.location, effectiveRange: nil) as? NSColor
    }

    private func resolved(_ color: NSColor, in appearance: NSAppearance) -> NSColor? {
        var resolvedColor: NSColor?
        appearance.performAsCurrentDrawingAppearance {
            resolvedColor = color.usingColorSpace(NSColorSpace.deviceRGB)
        }
        return resolvedColor
    }
}
#endif
