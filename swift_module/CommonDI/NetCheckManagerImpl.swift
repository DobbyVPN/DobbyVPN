import app
import MyLibrary

public class NetCheckManagerImpl: NetCheckManager {
    public func start() -> String {
        return Cloak_outlineNetCheck()
    }

    public func cancel() {
        Cloak_outlineCancelNetCheck()
    }
}
