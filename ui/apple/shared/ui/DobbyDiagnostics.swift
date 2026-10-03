import Foundation
#if canImport(DobbyDiagnosticFiles)
import DobbyDiagnosticFiles
#else
import IOSIntegration
#endif

func appendUIDiagnostic(_ message: String, to url: URL) throws {
    try DiagnosticFiles.append(message, event: "ui.failure", level: "ERROR", source: "native-ui", to: url)
}

func diagnosticPreview(paths: [URL]) -> (text: String, error: String) {
    DiagnosticFiles.preview(paths: paths)
}

func exportDiagnostics(paths: [URL], to url: URL, header: String) throws -> String {
    try DiagnosticFiles.export(paths: paths, to: url, header: header)
}
