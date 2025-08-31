import UIKit
import UniformTypeIdentifiers
import app

class CopyLogsInteractorImpl: CopyLogsInteractor {
    
    func doCopy(logs: [String]) {
        let logText = logs.joined(separator: "\n")
        
        let fileManager = FileManager.default
        let urls = fileManager.urls(for: .documentDirectory, in: .userDomainMask)
        
        guard let documentsDirectory = urls.first else {
            print("Не удалось найти Documents")
            return
        }
        
        // Формат даты: 2025-09-01_02-35-10
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd_HH-mm-ss"
        let dateString = formatter.string(from: Date())
        
        // Имя файла с датой
        let fileName = "DobbyVPN_logs_\(dateString).txt"
        let fileURL = documentsDirectory.appendingPathComponent(fileName)
        
        do {
            try logText.write(to: fileURL, atomically: true, encoding: .utf8)
            print("Логи сохранены в файл: \(fileURL.path)")
        } catch {
            print("Ошибка сохранения логов: \(error)")
        }
    }
}
