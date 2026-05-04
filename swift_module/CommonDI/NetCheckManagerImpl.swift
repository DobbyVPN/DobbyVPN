import app
import MyLibrary

public class NetCheckManagerImpl: NetCheckManager {
    public func startNetCheck() -> String {
        let configPath = NetCheckRepository_iosKt.provideNetCheckConfigPath().normalized().description()
        var err: NSError?
        Cloak_outlineNetCheck(configPath, &err)
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
