import UIKit
import UniformTypeIdentifiers
import app

class CopyLogsInteractorImpl: CopyLogsInteractor {
    private var logs = NativeModuleHolder.logsRepository
    
    func doCopy(logs: [String]) {
        let logText = logs.joined(separator: "\n")
        
        // Форматируем дату для имени файла
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd_HH-mm-ss"
        let dateString = formatter.string(from: Date())
        
        let fileName = "DobbyVPN_logs_\(dateString).txt"
        let fileURL = FileManager.default.temporaryDirectory.appendingPathComponent(fileName)
        
        do {
            try logText.write(to: fileURL, atomically: true, encoding: .utf8)
            self.logs.writeLog(log: "Логи сохранены во временный файл: \(fileURL.path)")
        } catch {
            self.logs.writeLog(log: "Ошибка сохранения логов: \(error.localizedDescription)")
            return
        }
        
        let activityVC = UIActivityViewController(activityItems: [fileURL], applicationActivities: nil)
        activityVC.excludedActivityTypes = [.assignToContact, .addToReadingList] // можно убрать ненужные
        
        if let topVC = topViewController() {
            topVC.present(activityVC, animated: true)
        } else {
            self.logs.writeLog(log: "Не удалось найти активный ViewController для показа UIActivityViewController")
        }
    }
    
    /// Находим topViewController из активной UIWindowScene
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
}
