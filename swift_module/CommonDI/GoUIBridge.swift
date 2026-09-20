import Foundation
import Darwin

// C-compatible bridge used by the Fyne application process. The Packet Tunnel
// remains the only process that links DobbyVPNRuntime; these calls forward the
// UI request through NetworkExtension to that process.
private func allocatedCString(_ value: String) -> UnsafeMutablePointer<CChar> {
    value.withCString { strdup($0)! }
}

private func stringFromCString(_ value: UnsafePointer<CChar>?) -> String {
    guard let value else { return "" }
    return String(cString: value)
}

@_cdecl("dobby_ui_startup")
public func dobbyUIStartup(_ mode: UnsafePointer<CChar>?) {
    let value = stringFromCString(mode)
    let startupMode = value.isEmpty ? "normal" : value
    IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.initialized mode=\(startupMode)")
}

@_cdecl("dobby_ui_attached")
public func dobbyUIAttached() {
    IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.ui_attached mode=normal")
}

@_cdecl("dobby_ui_diagnostic_paths")
public func dobbyUIDiagnosticPaths() -> UnsafeMutablePointer<CChar> {
    let paths = IOSAppCompositionRoot.diagnosticPaths().map(\.path)
    return allocatedCString(paths.joined(separator: "\n"))
}

@_cdecl("dobby_ui_configure")
public func dobbyUIConfigure(
    _ sessionID: UnsafePointer<CChar>?,
    _ expectedSequence: Int64,
    _ rawConfig: UnsafePointer<UInt8>?,
    _ rawLength: Int
) -> UnsafeMutablePointer<CChar> {
    guard rawLength >= 0, (rawLength == 0 || rawConfig != nil) else {
        return allocatedCString(IOSAppCompositionRoot.vpnManagerFailure(code: "MALFORMED_CONFIG", message: "configuration buffer is invalid"))
    }
    let raw = rawConfig.map { Data(bytes: $0, count: rawLength) } ?? Data()
    return allocatedCString(IOSAppCompositionRoot.sessionShell.configure(
        sessionID: stringFromCString(sessionID),
        expectedSequence: expectedSequence,
        rawConfig: raw
    ))
}

@_cdecl("dobby_ui_start")
public func dobbyUIStart(
    _ sessionID: UnsafePointer<CChar>?,
    _ expectedSequence: Int64,
    _ mode: UnsafePointer<CChar>?,
    _ index: Int32
) -> UnsafeMutablePointer<CChar> {
    allocatedCString(IOSAppCompositionRoot.sessionShell.start(
        sessionID: stringFromCString(sessionID),
        expectedSequence: expectedSequence,
        mode: stringFromCString(mode),
        index: index
    ))
}

@_cdecl("dobby_ui_stop")
public func dobbyUIStop(
    _ sessionID: UnsafePointer<CChar>?,
    _ generation: Int64
) -> UnsafeMutablePointer<CChar> {
    allocatedCString(IOSAppCompositionRoot.sessionShell.stop(
        sessionID: stringFromCString(sessionID), generation: generation
    ))
}

@_cdecl("dobby_ui_snapshot")
public func dobbyUISnapshot(_ sessionID: UnsafePointer<CChar>?) -> UnsafeMutablePointer<CChar> {
    allocatedCString(IOSAppCompositionRoot.sessionShell.snapshot(sessionID: stringFromCString(sessionID)))
}

@_cdecl("dobby_ui_reset")
public func dobbyUIReset(
    _ sessionID: UnsafePointer<CChar>?,
    _ expectedSequence: Int64
) -> UnsafeMutablePointer<CChar> {
    allocatedCString(IOSAppCompositionRoot.sessionShell.reset(
        sessionID: stringFromCString(sessionID), expectedSequence: expectedSequence
    ))
}

@_cdecl("dobby_ui_export_logs")
public func dobbyUIExportLogs(_ rawLogs: UnsafePointer<UInt8>?, _ rawLength: Int32) {
    guard rawLength >= 0, (rawLength == 0 || rawLogs != nil) else {
        IOSAppCompositionRoot.logsRepository.writeLog(log: "Log export failed: invalid buffer")
        return
    }
    let data = rawLogs.map { Data(bytes: $0, count: Int(rawLength)) } ?? Data()
    let text = String(decoding: data, as: UTF8.self)
    let logs = data.isEmpty ? [] : text.components(separatedBy: "\n")
    // This marker is intentionally content-free. It distinguishes a Fyne
    // tap/Go-to-Swift bridge failure from a UIKit presentation failure while
    // keeping exported configuration and diagnostic payloads private.
    IOSAppCompositionRoot.logsRepository.writeLog(log: "Log export requested")
    DispatchQueue.main.async {
        IOSAppCompositionRoot.exportLogsInteractor.export(logs: logs)
    }
}

@_cdecl("dobby_ui_free_string")
public func dobbyUIFreeString(_ value: UnsafeMutablePointer<CChar>?) {
    guard let value else { return }
    free(value)
}

extension IOSAppCompositionRoot {
    static func vpnManagerFailure(code: String, message: String) -> String {
        String(decoding: VpnManagerImpl.transportFailure(code, message: message), as: UTF8.self)
    }
}
