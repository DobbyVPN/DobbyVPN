import UIKit
import UniformTypeIdentifiers
import app
import zlib

class CopyLogsInteractorImpl: CopyLogsInteractor {
    private var logs = NativeModuleHolder.logsRepository

    func doCopy(logs: [String]) {
        let logText = logs.joined(separator: "\n")

        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd_HH-mm-ss"
        let dateString = formatter.string(from: Date())

        let fileName = "DobbyVPN_logs_\(dateString).txt.gz"
        let fileURL = FileManager.default.temporaryDirectory.appendingPathComponent(fileName)

        do {
            try writeGzip(logText, to: fileURL)
            self.logs.writeLog(log: "Write gzip logs in temporary file: \(fileURL.path)")
        } catch {
            self.logs.writeLog(log: "Error in log saving: \(error.localizedDescription)")
            return
        }

        let activityVC = UIActivityViewController(activityItems: [fileURL], applicationActivities: nil)
        activityVC.excludedActivityTypes = [.assignToContact, .addToReadingList]

        if let topVC = topViewController() {
            if let popover = activityVC.popoverPresentationController {
                popover.sourceView = topVC.view
                popover.sourceRect = CGRect(
                    x: topVC.view.bounds.midX,
                    y: topVC.view.bounds.midY,
                    width: 0,
                    height: 0
                )
                popover.permittedArrowDirections = []
            }
            topVC.present(activityVC, animated: true)
        } else {
            self.logs.writeLog(log: "Can't find active ViewController to view UIActivityViewController")
        }
    }

    private func topViewController() -> UIViewController? {
        guard let windowScene = UIApplication.shared.connectedScenes
                .compactMap({ $0 as? UIWindowScene })
                .first(where: { $0.activationState == .foregroundActive }),
              let window = windowScene.windows.first(where: { $0.isKeyWindow }),
              let root = window.rootViewController else {
            return nil
        }

        var top = root
        while let presented = top.presentedViewController {
            top = presented
        }
        return top
    }

    private func writeGzip(_ text: String, to fileURL: URL) throws {
        let data = Data(text.utf8)

        try fileURL.path.withCString { path in
            try "wb9".withCString { mode in
                guard let gzipFile = gzopen(path, mode) else {
                    throw NSError(
                        domain: "CopyLogsInteractorImpl",
                        code: 1,
                        userInfo: [NSLocalizedDescriptionKey: "Unable to open gzip file"]
                    )
                }
                defer {
                    gzclose(gzipFile)
                }

                if data.isEmpty {
                    return
                }

                let written = data.withUnsafeBytes { bytes -> Int32 in
                    guard let baseAddress = bytes.baseAddress else { return 0 }
                    return gzwrite(gzipFile, baseAddress, UInt32(data.count))
                }

                if written != Int32(data.count) {
                    throw NSError(
                        domain: "CopyLogsInteractorImpl",
                        code: 2,
                        userInfo: [NSLocalizedDescriptionKey: "Incomplete gzip write"]
                    )
                }
            }
        }
    }
}

public func maskStr(value: String) -> String {
    guard value.count > 2 else { return value }   // if length is 1-2, don't mask

    let first = value[value.startIndex]
    let last = value[value.index(before: value.endIndex)]
    return "\(first)***\(last)"
}
