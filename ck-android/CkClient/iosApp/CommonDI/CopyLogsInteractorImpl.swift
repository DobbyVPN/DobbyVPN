import UIKit
import UniformTypeIdentifiers

class CopyLogsInteractorImpl: CopyLogsInteractor {
    
    func doCopy(logs: [String]) {
        let logText = logs.joined(separator: "\n")
        let pasteboard = UIPasteboard.general

        if #available(iOS 14.0, *) {
            let provider = NSItemProvider(object: logText as NSString)
            provider.suggestedName = "logs.txt"
            pasteboard.setItemProviders([provider], localOnly: true, expirationDate: nil)
        } else {
            pasteboard.string = logText
        }

        print("Copied logs to iOS clipboard as text file")
    }
}
