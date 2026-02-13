import NetworkExtension
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public final class OutlineInteractor {
    func startOutline() {
        Cloak_outlineNewOutlineClient(, <#T##fd: Int##Int#>, <#T##error: NSErrorPointer##NSErrorPointer#>)
        Cloak_outlineOutlineConnect(<#T##error: NSErrorPointer##NSErrorPointer#>)
    }
    
    func stopOutline() {
        Cloak_outlineOutlineDisconnect(<#T##error: NSErrorPointer##NSErrorPointer#>)
    }
}
