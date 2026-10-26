import Foundation
#if canImport(DobbyDiagnosticFiles)
import DobbyDiagnosticFiles
#else
import IOSIntegration
#endif

func appendUIDiagnostic(
    _ message: String,
    to url: URL,
    event: String = "ui.failure",
    level: String = "ERROR"
) throws {
    try DiagnosticFiles.append(message, event: event, level: level, source: "native-ui", to: url)
}

func exportDiagnostics(paths: [URL], to url: URL, header: String) throws -> String {
    try DiagnosticFiles.export(paths: paths, to: url, header: header)
}

typealias DobbyLogEntry = DiagnosticFiles.Entry

/// DEBUG and TRACE stay in the files and the export; the screen shows what a person reads.
func isShownOnScreen(_ entry: DobbyLogEntry) -> Bool { entry.level != "DEBUG" && entry.level != "TRACE" }

func structuredPreview(paths: [URL], boundary: URL) -> (entries: [DobbyLogEntry], error: String) {
    DiagnosticFiles.entries(paths: paths, boundary: boundary)
}
func clearDiagnosticView(paths: [URL], boundary: URL) throws { try DiagnosticFiles.clearView(paths: paths, boundary: boundary) }
