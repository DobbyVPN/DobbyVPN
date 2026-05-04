import app
import MyLibrary

public class NetCheckManagerImpl: NetCheckManager {
    private static let netCheckPath = NetCheckRepository_iosKt.provideNetCheckConfigPath()

    public func startNetCheck() -> String {
        var err: NSError?
        Cloak_outlineNetCheck(netCheckPath, &err)
        if let error = err {
            return "failed to start netcheck: \(error.localizedDescription)"
        } else {
            return ""
        }
    }

    public func cancelNetCheck() {
        Cloak_outlineCancelNetCheck()
    }
}
