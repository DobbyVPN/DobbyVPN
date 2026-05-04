import app
import MyLibrary

public final class NetCheckManagerImpl: NetCheckManager {
    public func start() -> String {
        return Cloak_outlineNetCheck()
    }

    public func cancel() {
        Cloak_outlineCancelNetCheck()
    }
}
