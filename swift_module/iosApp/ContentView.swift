import UIKit
import SwiftUI
import CommonDI

struct ContentView: View {
    var body: some View {
        // The release application executable is the Go/Fyne target. This
        // Swift target is a small native diagnostics host used only when
        // opening the Xcode project directly; it does not own UI policy.
        Text("Dobby VPN")
            .accessibilityIdentifier("dobby.native-host")
    }
}
