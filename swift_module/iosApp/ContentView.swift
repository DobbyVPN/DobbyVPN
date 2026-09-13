import UIKit
import SwiftUI
import app
import CommonDI

struct ComposeView: UIViewControllerRepresentable {

    func makeUIViewController(context: Context) -> UIViewController {
        return MainViewControllerKt.MainViewController(dependencies: IOSAppCompositionRoot.appDependencies)
    }

    func updateUIViewController(_ uiViewController: UIViewController, context: Context) {}
}

struct ContentView: View {
    var body: some View {
        ComposeView()
            .ignoresSafeArea(.keyboard) // Compose has own keyboard handler
    }
}
