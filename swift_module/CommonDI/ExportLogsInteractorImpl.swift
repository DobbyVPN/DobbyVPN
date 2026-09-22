import UIKit
import zlib

public final class ExportLogsInteractorImpl: NSObject, UIAdaptivePresentationControllerDelegate, UIDocumentPickerDelegate {
    private let logs = IOSAppCompositionRoot.logsRepository
    private var exportPrompt: UIAlertController?
    private var activityViewController: UIActivityViewController?
    private var documentPicker: UIDocumentPickerViewController?
    private var isRestoringPresentation = false

    public func export(logs: [String]) {
        let logText = logs.joined(separator: "\n")

        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd_HH-mm-ss"
        let dateString = formatter.string(from: Date())

        let fileName = "DobbyVPN_logs_\(dateString).jsonl.gz"
        let fileURL = FileManager.default.temporaryDirectory.appendingPathComponent(fileName)

        do {
            try writeGzip(logText, to: fileURL)
            self.logs.writeLog(log: "Log export archive written to temporary storage")
        } catch {
            self.logs.writeLog(log: "Log export failed: \(String(reflecting: error))")
            return
        }

        present(fileURL: fileURL)
    }

    private func present(fileURL: URL) {
        if !Thread.isMainThread {
            // Go may invoke the C bridge from its event goroutine. UIKit scene
            // construction, scene, and presentation APIs are main-thread-only;
            // keep this guard at the native boundary so every caller gets the
            // same guarantee.
            DispatchQueue.main.async { [self] in
                present(fileURL: fileURL)
            }
            return
        }

        guard exportPrompt == nil, activityViewController == nil, documentPicker == nil else {
            logs.writeLog(log: "Log export presentation already active")
            return
        }

        guard let active = activePresenter() else {
            logs.writeLog(log: "Can't find active app presenter to view log export options")
            return
        }

        let prompt = UIAlertController(
            title: "Export logs",
            message: "Choose how to export the compressed log archive.",
            preferredStyle: .alert
        )
        prompt.addAction(UIAlertAction(title: "Share", style: .default) { [weak self, weak prompt] _ in
            guard let self, let prompt else { return }
            self.continueAfterPromptDismissal(prompt) { [weak self] in
                self?.presentActivityViewController(fileURL: fileURL)
            }
        })
        prompt.addAction(UIAlertAction(title: "Save", style: .default) { [weak self, weak prompt] _ in
            guard let self, let prompt else { return }
            self.logs.writeLog(log: "Log export Save action selected")
            self.continueAfterPromptDismissal(prompt) { [weak self] in
                self?.presentDocumentPicker(fileURL: fileURL)
            }
        })
        prompt.addAction(UIAlertAction(title: "Close", style: .cancel) { [weak self] _ in
            self?.restorePresentationState()
        })

        self.exportPrompt = prompt
        active.viewController.present(prompt, animated: true)
    }

    private func continueAfterPromptDismissal(_ prompt: UIAlertController, action: @escaping () -> Void) {
        let finish = { [weak self, weak prompt] in
            if self?.exportPrompt === prompt {
                self?.exportPrompt = nil
            }
            self?.logs.writeLog(log: "Log export prompt dismissal completed")
            action()
        }
        if prompt.presentingViewController != nil {
            logs.writeLog(log: "Log export prompt dismissal requested")
            prompt.dismiss(animated: true, completion: finish)
        } else {
            logs.writeLog(log: "Log export prompt already detached")
            DispatchQueue.main.async(execute: finish)
        }
    }

    private func presentActivityViewController(fileURL: URL) {
        guard Thread.isMainThread else {
            DispatchQueue.main.async { [weak self] in
                self?.presentActivityViewController(fileURL: fileURL)
            }
            return
        }

        guard activityViewController == nil, documentPicker == nil else {
            logs.writeLog(log: "Log export presentation already active")
            return
        }
        let activityVC = UIActivityViewController(activityItems: [fileURL], applicationActivities: nil)
        activityVC.excludedActivityTypes = [.assignToContact, .addToReadingList]
        self.activityViewController = activityVC
        activityVC.completionWithItemsHandler = { [weak self] _, _, _, _ in
            self?.restorePresentationState()
        }

        // Present from the active Go/Fyne window's topmost controller. A
        // separate transparent key window can complete the UIKit transition
        // while keeping the activity UI outside the app's discoverable window
        // hierarchy on headless Simulators.
        DispatchQueue.main.async { [weak self, weak activityVC] in
            guard let self else { return }
            guard let activityVC,
                  self.activityViewController === activityVC,
                  let active = self.activePresenter() else {
                self.restorePresentationState()
                return
            }

            if let popover = activityVC.popoverPresentationController {
                popover.sourceView = active.view
                popover.sourceRect = CGRect(
                    x: active.view.bounds.midX,
                    y: active.view.bounds.midY,
                    width: 0,
                    height: 0
                )
                popover.permittedArrowDirections = []
            }

            active.viewController.present(activityVC, animated: true) { [weak self, weak activityVC] in
                activityVC?.presentationController?.delegate = self
                self?.logs.writeLog(log: "Log export presentation transition completed")
            }
            activityVC.presentationController?.delegate = self
        }
    }

    private func presentDocumentPicker(fileURL: URL) {
        guard Thread.isMainThread else {
            DispatchQueue.main.async { [weak self] in
                self?.presentDocumentPicker(fileURL: fileURL)
            }
            return
        }

        guard activityViewController == nil, documentPicker == nil else {
            logs.writeLog(log: "Log export presentation already active")
            return
        }
        guard let active = activePresenter() else {
            logs.writeLog(log: "Can't find active app presenter to save log export")
            restorePresentationState()
            return
        }

        logs.writeLog(log: "Log export document picker presentation requested")
        let picker = UIDocumentPickerViewController(forExporting: [fileURL], asCopy: true)
        picker.delegate = self
        self.documentPicker = picker
        if let popover = picker.popoverPresentationController {
            popover.sourceView = active.view
            popover.sourceRect = CGRect(
                x: active.view.bounds.midX,
                y: active.view.bounds.midY,
                width: 0,
                height: 0
            )
            popover.permittedArrowDirections = []
        }
        active.viewController.present(picker, animated: true) { [weak self, weak picker] in
            self?.logs.writeLog(
                log: "Log export document picker presentation completed "
                    + "presented=\(picker?.presentingViewController != nil)"
            )
        }
    }

    private func activeWindowScene() -> UIWindowScene? {
        let scenes = UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .sorted { left, right in
                let leftActive = left.activationState == .foregroundActive
                let rightActive = right.activationState == .foregroundActive
                return leftActive && !rightActive
            }
        return scenes.first(where: {
            $0.activationState == .foregroundActive || $0.activationState == .foregroundInactive
        })
    }

    private func activeWindow(in scene: UIWindowScene) -> UIWindow? {
        let windows = scene.windows.filter {
            !$0.isHidden && $0.alpha > 0 && $0.rootViewController != nil
        }
        return windows.first(where: { $0.isKeyWindow }) ?? windows.first
    }

    private func activePresenter() -> (window: UIWindow, viewController: UIViewController, view: UIView)? {
        guard let scene = activeWindowScene(),
              let window = activeWindow(in: scene),
              let rootViewController = window.rootViewController,
              !window.isHidden,
              window.alpha > 0 else {
            return nil
        }

        let presenter = topViewController(from: rootViewController)
        presenter.loadViewIfNeeded()
        guard let view = presenter.viewIfLoaded,
              view.window === window,
              !presenter.isBeingDismissed,
              !presenter.isBeingPresented else {
            return nil
        }
        return (window, presenter, view)
    }

    private func topViewController(from root: UIViewController) -> UIViewController {
        if let presented = root.presentedViewController, !presented.isBeingDismissed {
            return topViewController(from: presented)
        }
        if let navigation = root as? UINavigationController,
           let visible = navigation.visibleViewController {
            return topViewController(from: visible)
        }
        if let tab = root as? UITabBarController,
           let selected = tab.selectedViewController {
            return topViewController(from: selected)
        }
        if let split = root as? UISplitViewController,
           let visible = split.viewControllers.last {
            return topViewController(from: visible)
        }
        if let child = root.children.reversed().first(where: { $0.viewIfLoaded?.window != nil }) {
            return topViewController(from: child)
        }
        return root
    }

    private func restorePresentationState() {
        if !Thread.isMainThread {
            DispatchQueue.main.async { [weak self] in
                self?.restorePresentationState()
            }
            return
        }

        // The export prompt, activity controller, and document picker can each
        // report completion/dismissal. Make cleanup idempotent without changing
        // the active app window or its Fyne root controller.
        guard !isRestoringPresentation else { return }
        isRestoringPresentation = true
        exportPrompt?.dismiss(animated: false)
        exportPrompt = nil
        self.activityViewController = nil
        self.documentPicker = nil
        isRestoringPresentation = false
    }

    public func presentationControllerDidDismiss(_ presentationController: UIPresentationController) {
        restorePresentationState()
    }

    public func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        restorePresentationState()
    }

    public func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        restorePresentationState()
    }

    private func writeGzip(_ text: String, to fileURL: URL) throws {
        try fileURL.path.withCString { path in
            try "wb9".withCString { mode in
                guard let gzipFile = gzopen(path, mode) else {
                    throw NSError(
                        domain: "ExportLogsInteractorImpl",
                        code: 1,
                        userInfo: [NSLocalizedDescriptionKey: "Unable to open gzip file"]
                    )
                }

                let chunkSize = 64 * 1024
                let writeChunk: ([UInt8]) throws -> Void = { chunk in
                    let count = chunk.count
                    let written = chunk.withUnsafeBytes { bytes -> Int32 in
                        guard let baseAddress = bytes.baseAddress else { return 0 }
                        return gzwrite(gzipFile, baseAddress, UInt32(count))
                    }
                    if written != Int32(count) {
                        throw NSError(
                            domain: "ExportLogsInteractorImpl",
                            code: 2,
                            userInfo: [
                                NSLocalizedDescriptionKey:
                                    "Incomplete gzip write (wrote \(written) of \(count) bytes)"
                            ]
                        )
                    }
                }
                var writeError: Error?
                do {
                    var chunk: [UInt8] = []
                    chunk.reserveCapacity(chunkSize)
                    for byte in text.utf8 {
                        chunk.append(byte)
                        if chunk.count == chunkSize {
                            try writeChunk(chunk)
                            chunk.removeAll(keepingCapacity: true)
                        }
                    }
                    if !chunk.isEmpty {
                        try writeChunk(chunk)
                    }
                } catch {
                    writeError = error
                }

                let closeStatus = gzclose(gzipFile)
                if let writeError {
                    if closeStatus != 0 {
                        throw NSError(
                            domain: "ExportLogsInteractorImpl",
                            code: 4,
                            userInfo: [
                                NSLocalizedDescriptionKey:
                                    "Gzip write failed and close returned status \(closeStatus)",
                                NSUnderlyingErrorKey: writeError,
                            ]
                        )
                    }
                    throw writeError
                }
                if closeStatus != 0 {
                    throw NSError(
                        domain: "ExportLogsInteractorImpl",
                        code: 3,
                        userInfo: [
                            NSLocalizedDescriptionKey:
                                "Gzip close failed with status \(closeStatus)"
                        ]
                    )
                }
            }
        }
    }
}
