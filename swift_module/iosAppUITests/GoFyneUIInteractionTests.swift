import XCTest

/// UI-only proof for the installed Go/Fyne Simulator bundle.
///
/// This target deliberately does not import the Swift diagnostics host or
/// claim NetworkExtension/VPN success. The app under test is the bundle that
/// the Simulator packaging contract installed (`vpn.dobby.app`); XCTest is
/// used only for accessibility lookup, native taps, and lifecycle.
final class GoFyneUIInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")
    private let connectionActionLabel = "VPN connection action"

    override func setUpWithError() throws {
        continueAfterFailure = false
        // The Python Simulator lane installs the bundle but deliberately does
        // not launch it. Let XCTest own the first launch; synchronously
        // terminating a process launched by simctl can block XCTest before the
        // first test action is reported.
        app.launch()
    }

    func testGoFyneMiniJourney() throws {
        let input = element(named: "Connection configuration")
        XCTAssertTrue(input.waitForExistence(timeout: 30), "Fyne configuration input is not accessible")

        // Keep the Simulator lane as one rendered journey. Each XCTest method
        // starts a fresh Go/Fyne process; splitting these checks into five
        // methods made the class spend most of its 600-second budget on cold
        // starts rather than product behavior.
        let settings = element(named: "Settings")
        XCTAssertTrue(settings.waitForExistence(timeout: 10), "Fyne Settings control is not accessible")
        settings.tap()

        XCTAssertTrue(
            element(withLabelPrefix: "Version:").waitForExistence(timeout: 10),
            "Settings did not expose the release version"
        )
        XCTAssertTrue(
            element(withLabelPrefix: "Source commit:").waitForExistence(timeout: 10),
            "Settings did not expose the source commit link"
        )

        let back = element(named: "Back")
        XCTAssertTrue(back.waitForExistence(timeout: 10), "Fyne Back control is not accessible")
        back.tap()
        let inputAfterSettings = element(named: "Connection configuration")
        XCTAssertTrue(
            inputAfterSettings.waitForExistence(timeout: 30),
            "Go/Fyne UI did not return from Settings"
        )
        let connect = element(named: connectionActionLabel)
        XCTAssertTrue(connect.waitForExistence(timeout: 10), "Fyne connection action is not accessible")
        XCTAssertTrue(waitForEnabled(connect), "Fyne connection action is not enabled after startup")

        // Exercise real keyboard taps, editing, clearing, and a malformed
        // connect without depending on a renderer-specific accessibility value.
        // A frame-anchored coordinate sends a real screen tap through Fyne's
        // GL view. Resolve each current software-keyboard key from the
        // accessibility tree, then tap its frame center so XCTest dispatches
        // the same key event a user would produce.
        editConfiguration(inputAfterSettings, text: "bad")

        // Exercise keyboard editing before clearing the value. The exact
        // accessibility value is renderer-dependent, so the subsequent
        // malformed-configuration result is the portable proof that the
        // edited value reached the production Connect callback.
        dismissSoftwareKeyboard()
        editConfiguration(inputAfterSettings, deleteCount: 1)
        dismissSoftwareKeyboard()
        editConfiguration(inputAfterSettings, text: "x")
        dismissSoftwareKeyboard()
        editConfiguration(inputAfterSettings, deleteCount: 3)
        dismissSoftwareKeyboard()
        editConfiguration(inputAfterSettings, text: "bad")

        dismissSoftwareKeyboard()
        XCTAssertFalse(connect.frame.isEmpty, "Fyne Connect control has no tappable frame after keyboard dismissal")
        XCTAssertTrue(
            tapConnectExpectingFailure(connect),
            "Connect did not produce a visible malformed-input failure state"
        )
        XCTAssertFalse(element(named: "Connected").exists, "Simulator UI must not claim a connected VPN")

        // The malformed inline fixture is deliberately not persisted. Restart
        // immediately after the failed action so the error state never needs
        // to refocus Fyne's native input responder.
        app.terminate()
        app.launch()
        let reopened = element(named: "Connection configuration")
        XCTAssertTrue(reopened.waitForExistence(timeout: 30), "Go/Fyne UI did not reopen after termination")
        // A custom Fyne accessibility element may not expose its text value;
        // an optional `value` check alone cannot prove that the inline value
        // was not restored. Keep it as an extra signal when available, but
        // make the production empty-input action the authoritative assertion.
        if let value = reopened.value as? String {
            XCTAssertFalse(value.contains("bad"), "unaccepted inline configuration was restored")
        }

        // RunWithDiagnosticStore starts the mobile watcher from Fyne's native
        // lifecycle callback, after the first window is shown. Waiting only
        // for the text element lets XCTest race that callback on a fast
        // relaunch: the button is rendered, but its production action is not
        // ready to dispatch. Require the freshly rendered action to be enabled
        // before submitting the empty-input request.
        let reopenedConnect = element(named: connectionActionLabel)
        XCTAssertTrue(reopenedConnect.waitForExistence(timeout: 30), "Go/Fyne connection action did not reopen")
        XCTAssertTrue(waitForEnabled(reopenedConnect), "Go/Fyne connection action was not ready after relaunch")
        XCTAssertTrue(
            element(named: "Connection logs").waitForExistence(timeout: 10),
            "Go/Fyne diagnostics view did not reopen"
        )
        // Clear logs is hidden until the deferred native DiagnosticStore is
        // injected. Its rendered presence is the stable product-owned
        // readiness point for the mobile-start callback; a merely visible
        // Connect button is not sufficient because the window is shown first.
        XCTAssertTrue(
            element(named: "Clear logs").waitForExistence(timeout: 30),
            "Go/Fyne native diagnostic store did not become ready after relaunch"
        )

        // Prove non-persistence through the rendered product behavior, not
        // through an accessibility value that this custom Entry may omit. Do
        // not refocus the input after the first error: a restored malformed
        // value would produce the malformed-input error, while a correctly
        // empty reopened input produces the required-configuration error.
        XCTAssertTrue(
            tapConnectExpectingFailure(reopenedConnect),
            "reopened empty configuration did not produce a visible error"
        )
        let reopenedDetails = app.descendants(matching: .any).matching(
            NSPredicate(format: "label CONTAINS[c] %@", "required")
        ).firstMatch
        XCTAssertTrue(
            reopenedDetails.waitForExistence(timeout: 5),
            "reopened empty-input error did not explain that configuration is required"
        )
        XCTAssertFalse(element(named: "Connected").exists, "reopened empty configuration must not claim a connected VPN")

        let logs = element(named: "Connection logs")
        XCTAssertTrue(logs.waitForExistence(timeout: 10), "production app did not expose connection logs")

        let clear = element(named: "Clear logs")
        XCTAssertTrue(clear.waitForExistence(timeout: 10), "production app did not expose Clear logs")
        XCTAssertTrue(clear.isEnabled, "Clear logs control is unexpectedly disabled")
        clear.tap()
        let cleared = expectation(
            for: NSPredicate(format: "value == %@", ""),
            evaluatedWith: logs
        )
        wait(for: [cleared], timeout: 10)

        let export = element(named: "Export logs")
        XCTAssertTrue(export.waitForExistence(timeout: 10), "production app did not expose log export")
        XCTAssertTrue(export.isEnabled, "iOS log export control is unexpectedly disabled")
        XCTAssertFalse(export.frame.isEmpty, "Fyne Export logs control has no tappable frame")
        // Fyne's exported accessibility element can report an invalid
        // accessibility hit point after XCTest scrolls it into view. A
        // native element tap is preferred; the frame-anchored tap is a
        // bounded fallback for Simulator runtimes that expose the virtual
        // Fyne element without a reliable hit point. The resulting app-owned
        // UIKit prompt must expose real Share/Save/Close actions; Save then
        // exercises the supported native document-picker flow.
        dismissExportPrompt(returningTo: export)
    }

    private func waitForFailureState() -> Bool {
        let failure = app.descendants(matching: .any).matching(
            NSPredicate(format: "label == 'Error' OR label == 'Failed' OR identifier == 'Error' OR identifier == 'Failed'")
        ).firstMatch
        return failure.waitForExistence(timeout: 15)
    }

    private func tapConnectExpectingFailure(_ connect: XCUIElement) -> Bool {
        // Prefer XCTest's native accessibility action. Some no-Metal Fyne
        // controls expose a label but return an invalid action point after
        // XCTest's visibility scroll, so the action can be a no-op while the
        // element still looks tappable.
        connect.tap()
        if waitForFailureState() {
            return true
        }

        // Only recover after the expected rendered failure did not appear.
        // This is a real screen-coordinate tap derived from the current
        // rendered element frame, not a direct callback or text injection.
        guard !connect.frame.isEmpty else {
            return false
        }
        connect.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        return waitForFailureState()
    }

    private func waitForEnabled(_ element: XCUIElement, timeout: TimeInterval = 30) -> Bool {
        guard element.waitForExistence(timeout: timeout) else { return false }
        let enabled = expectation(
            for: NSPredicate(format: "isEnabled == true"),
            evaluatedWith: element
        )
        return XCTWaiter.wait(for: [enabled], timeout: timeout) == .completed
    }

    private func element(named name: String) -> XCUIElement {
        let predicate = NSPredicate(format: "label == %@ OR identifier == %@", name, name)
        return app.descendants(matching: .any).matching(predicate).firstMatch
    }

    private func element(withLabelPrefix prefix: String) -> XCUIElement {
        let predicate = NSPredicate(format: "label BEGINSWITH %@ OR identifier BEGINSWITH %@", prefix, prefix)
        return app.descendants(matching: .any).matching(predicate).firstMatch
    }

    private func editConfiguration(
        _ input: XCUIElement,
        text: String = "",
        deleteCount: Int = 0
    ) {
        let keyLabels = Array(text).map(String.init)
        let deletes = max(deleteCount, 0)
        let totalKeys = deletes + keyLabels.count
        XCTAssertGreaterThan(totalKeys, 0, "configuration edit must contain a key event")

        // Focus the freshly rendered input once for this edit operation. Each
        // key event can invalidate the prior keyboard subtree, so reacquire
        // the current keyboard/key objects with bounded waits. A Simulator
        // can leave a stale keyboard accessibility object behind after a
        // real key tap; after a short bounded retry window, refocus the
        // rendered input and resolve the live keyboard again. No fixed sleep
        // or XCTest text injection is used.
        let editDeadline = Date().addingTimeInterval(90)
        var focusInput = input
        func focusCurrentInput(until deadline: Date) -> Bool {
            while Date() < deadline && Date() < editDeadline {
                let currentInput = focusInput
                let inputTimeout = min(5, max(0.1, min(deadline.timeIntervalSinceNow, editDeadline.timeIntervalSinceNow)))
                guard currentInput.waitForExistence(timeout: inputTimeout) else {
                    focusInput = element(named: "Connection configuration")
                    continue
                }
                guard !currentInput.frame.isEmpty else {
                    focusInput = element(named: "Connection configuration")
                    continue
                }
                currentInput.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
                return true
            }
            return false
        }

        func resetSoftwareKeyboard(until deadline: Date) {
            // On no-Metal Simulators XCTest can retain the parent keyboard
            // accessibility object after a real key tap while its key
            // children have gone stale. A rendered tap outside Fyne's Entry
            // makes the production responder resign; refocusing below then
            // asks UIKit for a fresh keyboard tree. Keep the disappearance
            // wait short and bounded because the object itself may be stale.
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.06)).tap()
            let keyboard = app.keyboards.firstMatch
            let timeout = min(3, max(0.1, min(deadline.timeIntervalSinceNow, editDeadline.timeIntervalSinceNow)))
            guard timeout > 0 else { return }
            let gone = expectation(
                for: NSPredicate(format: "exists == false"),
                evaluatedWith: keyboard
            )
            _ = XCTWaiter.wait(for: [gone], timeout: timeout)
        }

        guard focusCurrentInput(until: min(Date().addingTimeInterval(15), editDeadline)) else {
            XCTFail("Fyne configuration input did not become focusable")
            return
        }

        for index in 0..<totalKeys {
            let isDelete = index < deletes
            let label = isDelete ? nil : keyLabels[index - deletes]
            let deadline = min(Date().addingTimeInterval(30), editDeadline)
            var tapped = false
            var needsFocus = false
            var unavailableKeyAttempts = 0

            while Date() < deadline && !tapped {
                if needsFocus {
                    guard focusCurrentInput(until: deadline) else { break }
                    needsFocus = false
                }

                let keyboard = app.keyboards.firstMatch
                let keyboardTimeout = min(15, max(0.1, min(deadline.timeIntervalSinceNow, editDeadline.timeIntervalSinceNow)))
                guard keyboard.waitForExistence(timeout: keyboardTimeout) else {
                    // The keyboard really disappeared; refocus the current
                    // rendered input before trying to resolve it again.
                    needsFocus = true
                    continue
                }

                let predicate: NSPredicate
                if let label {
                    predicate = NSPredicate(
                        format: "label ==[c] %@ OR identifier ==[c] %@",
                        label,
                        label
                    )
                } else {
                    predicate = NSPredicate(
                        format: "label CONTAINS[c] %@ OR identifier CONTAINS[c] %@",
                        "delete",
                        "delete"
                    )
                }
                // Resolve the key from the stable application root. A real
                // key tap can rebuild or dismiss the intermediate Keyboard
                // accessibility node before the next loop iteration; a
                // query chained through that vanished parent makes XCTest
                // abort instead of returning control to the bounded recovery.
                let key = app.keys.matching(predicate).firstMatch
                // Keep this short enough to recover before the per-key
                // deadline. Fyne's no-Metal keyboard may report the parent
                // keyboard as existing while rebuilding its key children.
                let keyTimeout = min(1, max(0.1, min(deadline.timeIntervalSinceNow, editDeadline.timeIntervalSinceNow)))
                let keyboardFrame = keyboard.frame
                guard keyboard.exists,
                    !keyboardFrame.isEmpty,
                    key.waitForExistence(timeout: keyTimeout),
                    !key.frame.isEmpty,
                    keyboardFrame.intersects(key.frame) else {
                    // A key subtree can be stale while the parent keyboard
                    // still reports exists=true. Give a live subtree a few
                    // quick opportunities to appear, then refocus the
                    // current rendered input after a real responder reset so
                    // XCTest does not keep querying an obsolete
                    // remote-keyboard snapshot forever.
                    unavailableKeyAttempts += 1
                    if unavailableKeyAttempts >= 3 || !keyboard.exists || keyboard.frame.isEmpty {
                        resetSoftwareKeyboard(until: deadline)
                        needsFocus = true
                        unavailableKeyAttempts = 0
                    }
                    continue
                }
                unavailableKeyAttempts = 0

                // The accessibility action point for Fyne's keyboard keys is
                // unreliable on no-Metal Simulators. A frame-centered tap
                // dispatches the real key event without asking XCTest to
                // scroll or activate the virtual key action.
                key.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
                tapped = true
            }

            XCTAssertTrue(
                tapped,
                "software keyboard key did not become tappable for "
                    + (isDelete ? "delete" : label ?? "unknown")
            )
        }
    }

    private func dismissSoftwareKeyboard() {
        // A coordinate tap in the rendered GL view resigns Fyne's native
        // responder after the final physical key event. Wait for the real
        // keyboard to leave the accessibility tree before the next edit
        // refocuses the Entry; iOS 26 can otherwise retain an empty keyboard
        // container whose key children never become tappable.
        let keyboard = app.keyboards.firstMatch
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.06)).tap()
        let gone = expectation(
            for: NSPredicate(format: "exists == false"),
            evaluatedWith: keyboard
        )
        _ = XCTWaiter.wait(for: [gone], timeout: 5)
    }

    private func dismissExportPrompt(returningTo export: XCUIElement) {
        let owners: [(String, XCUIApplication)] = [
            // Keep the app first because iOS 26 hosts the app-owned export
            // prompt in the product process.
            ("DobbyVPN", app),
            // UIDocumentPickerViewController may be hosted by the app or by
            // one of the Files/FileProvider system owners. On iOS 26 the
            // picker is hosted in this app-owned UI scene extension; it is
            // not discoverable through the DocumentsApp bundle or the app's
            // own accessibility tree.
            ("DocumentManagerUICore", XCUIApplication(bundleIdentifier: "com.apple.DocumentManagerUICore.Service")),
            ("Files", XCUIApplication(bundleIdentifier: "com.apple.DocumentsApp")),
            ("FileProviderUI", XCUIApplication(bundleIdentifier: "com.apple.fileproviderui")),
            ("SpringBoard", XCUIApplication(bundleIdentifier: "com.apple.springboard")),
        ]
        // The production export entry point is an app-owned UIKit prompt. The
        // prompt's Share/Save/Close controls are the supported Simulator
        // boundary; exercise Save as the representative native export flow.
        let tapDeadline = Date().addingTimeInterval(20)
        export.tap()
        if !waitForNativeExportPrompt(in: app, owners: owners, until: tapDeadline) {
            // A virtual Fyne button can expose a usable label but an unusable
            // XCTest action point. A second physical frame tap is limited to
            // the no-prompt case.
            export.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
            let deadline = Date().addingTimeInterval(20)
            guard waitForNativeExportPrompt(in: app, owners: owners, until: deadline) else {
                XCTFail("native log export prompt did not present Share, Save, and Close")
                return
            }
        }
    }

    private func waitForNativeExportPrompt(
        in owner: XCUIApplication,
        owners: [(String, XCUIApplication)],
        until deadline: Date
    ) -> Bool {
        while Date() < deadline {
            let surfaces = [owner.alerts.firstMatch, owner.sheets.firstMatch]
            for surface in surfaces {
                let timeout = min(0.5, max(0.1, deadline.timeIntervalSinceNow))
                guard surface.waitForExistence(timeout: timeout) else { continue }

                let share = surface.buttons["Share"]
                let save = surface.buttons["Save"]
                let close = surface.buttons["Close"]
                XCTAssertTrue(share.waitForExistence(timeout: 2), "native export prompt did not expose Share")
                XCTAssertTrue(save.waitForExistence(timeout: 2), "native export prompt did not expose Save")
                XCTAssertTrue(close.waitForExistence(timeout: 2), "native export prompt did not expose Close")
                // Exercise the supported native export path. Canceling the
                // document picker returns to the same Fyne window without
                // accepting or exporting a file.
                save.tap()
                let pickerDeadline = Date().addingTimeInterval(20)
                let pickerCompleted = waitForDocumentPickerAndCancel(in: owners, until: pickerDeadline)
                XCTAssertTrue(
                    pickerCompleted,
                    "native log export Save action did not present a dismissible document picker"
                )
                return true
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return false
    }

    private func waitForDocumentPickerAndCancel(
        in owners: [(String, XCUIApplication)],
        until deadline: Date
    ) -> Bool {
        while Date() < deadline {
            for owner in owners {
                // iOS 26's DocumentManagerUICore scene can expose the native
                // picker actions as non-Button accessibility elements across
                // the remote scene boundary. Resolve the semantic controls
                // by label, identifier, or value in the complete native subtree;
                // this remains a real UIKit control lookup, not a test-only
                // replacement or a synthetic tap target.
                let cancel = documentPickerControl(owner.1, named: "Cancel")
                guard cancel.waitForExistence(timeout: 0.2) else { continue }

                // A navigation bar plus Cancel/Save is the stable native
                // UIDocumentPicker surface across current Simulator hosts;
                // it avoids retaining raw hierarchy or log evidence.
                let navigationBar = owner.1.navigationBars.firstMatch
                guard navigationBar.waitForExistence(timeout: 0.2) else { continue }
                let save = documentPickerControl(owner.1, named: "Save")
                guard save.waitForExistence(timeout: 0.2) else { continue }
                guard !cancel.frame.isEmpty, !save.frame.isEmpty else { continue }
                cancel.tap()
                return waitForDocumentPickerReturn(
                    after: cancel,
                    owner: owner.1,
                    message: "native document picker remained visible after tapping Cancel"
                )
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return false
    }

    private func documentPickerControl(_ owner: XCUIApplication, named name: String) -> XCUIElement {
        let semanticLabel = NSPredicate(
            // DocumentManagerUICore crosses an accessibility-process boundary
            // on iOS 26. Its toolbar actions are exposed as generic elements
            // and, depending on the current document-browser scene, the
            // visible title is published as `value` rather than `label`.
            // Include all user-facing semantic attributes while retaining a
            // real element lookup (never a coordinate-only fallback).
            format: "label ==[c] %@ OR identifier ==[c] %@ OR value ==[c] %@ OR label CONTAINS[c] %@ OR identifier CONTAINS[c] %@ OR value CONTAINS[c] %@",
            name,
            name,
            name,
            name,
            name,
            name
        )
        return owner.descendants(matching: .any).matching(semanticLabel).firstMatch
    }

    private func waitForDocumentPickerReturn(
        after dismissedElement: XCUIElement,
        owner: XCUIApplication,
        message: String
    ) -> Bool {
        // A dismissed remote UIKit scene can leave the original XCUIElement
        // proxy reporting `exists == true` indefinitely after its process has
        // gone away. Require the Go/Fyne app to return to the foreground and
        // the picker control to stop being hittable instead.
        let export = element(named: "Export logs")
        let appReturned = export.waitForExistence(timeout: 5)
            && !export.frame.isEmpty
            && app.wait(for: .runningForeground, timeout: 5)
        let pickerReturned = waitForNativeSurfaceDismissal(
            dismissedElement,
            owner: owner,
            until: Date().addingTimeInterval(5)
        )

        XCTAssertTrue(appReturned, "export dismissal did not return to the Go/Fyne Export logs control")
        XCTAssertTrue(pickerReturned, message)
        return appReturned && pickerReturned
    }

    private func waitForNativeSurfaceDismissal(
        _ dismissedElement: XCUIElement,
        owner: XCUIApplication,
        until deadline: Date
    ) -> Bool {
        while Date() < deadline {
            // `isHittable` is a live interaction property and is more useful
            // here than `exists` for an element proxy retained across a remote
            // scene teardown. A system picker may also leave its service in a
            // short-lived background state, so accept that independent signal.
            if !dismissedElement.isHittable || owner.state != .runningForeground {
                return true
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return !dismissedElement.isHittable || owner.state != .runningForeground
    }
}
