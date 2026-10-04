import Foundation
#if canImport(DobbyDiagnosticFiles)
import DobbyDiagnosticFiles
#else
import IOSIntegration
#endif

func appendUIDiagnostic(_ message: String, to url: URL) throws {
    try DiagnosticFiles.append(message, event: "ui.failure", level: "ERROR", source: "native-ui", to: url)
}

func exportDiagnostics(paths: [URL], to url: URL, header: String) throws -> String {
    try DiagnosticFiles.export(paths: paths, to: url, header: header)
}

typealias DobbyLogEntry = DiagnosticFiles.Entry

func structuredPreview(paths: [URL], boundary: URL) -> (entries: [DobbyLogEntry], error: String) {
    DiagnosticFiles.entries(paths: paths, boundary: boundary)
}
func clearDiagnosticView(paths: [URL], boundary: URL) throws { try DiagnosticFiles.clearView(paths: paths, boundary: boundary) }
