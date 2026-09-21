from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


def test_hosted_driver_reads_the_current_session_before_configuring() -> None:
    source = (
        ROOT
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    attach = source.index("NativeGoSession.attach(context);")
    snapshot = source.index('JSONObject initial = snapshotResult("");', attach)
    session = source.index('sessionID = initial.getString("session_id");', snapshot)
    sequence = source.index('sequence = initial.getLong("sequence");', session)
    configure = source.index("NativeGoSession.configure(sessionID, sequence, profile)", sequence)

    assert attach < snapshot < session < sequence < configure


def test_workflow_proves_android_routing_chain_cleanup() -> None:
    source = (ROOT / ".github/workflows/qualification_platform.yml").read_text(
        encoding="utf-8"
    )
    step = source.index("- name: Remove Android qualification routing rules")
    always = source.index("if: always() && inputs.platform == 'android'", step)
    remove = source.index(
        "iptables -D OUTPUT -j DOBBYVPN_TORTURER", always
    )
    flush = source.index("iptables -F DOBBYVPN_TORTURER", remove)
    delete = source.index("iptables -X DOBBYVPN_TORTURER", flush)
    inventory = source.index('inventory="$(iptables -S)" || exit $?', delete)
    residual = source.index("*DOBBYVPN_TORTURER*)", inventory)
    failure = source.index("exit 1", residual)

    assert step < always < remove < flush < delete < inventory < residual < failure


def test_workflow_does_not_collect_android_diagnostic_files() -> None:
    source = (ROOT / ".github/workflows/qualification_platform.yml").read_text(
        encoding="utf-8"
    )
    assert "- name: Collect Android logs" not in source
    assert "/files/diagnostics/" not in source
    assert "adb pull" not in source
    assert "dobbyvpn-ui-failure.png" not in source
    assert "dobbyvpn-ui-failure.xml" not in source


def test_android_failure_diagnostics_keep_only_fixed_result_vocabulary() -> None:
    root = Path(__file__).resolve().parents[2]
    kotlin = (
        root
        / "android_module/app/src/androidTest/kotlin/com/dobby/GoUiInstrumentedTest.kt"
    ).read_text(encoding="utf-8")
    local = (
        root / "torturer/torturer_checks/local_vm_android.py"
    ).read_text(encoding="utf-8")
    java = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")
    for source in (kotlin, local, java):
        assert "dobbyvpn-ui-failure.png" not in source
        assert "dobbyvpn-ui-failure.xml" not in source
        assert "dumpWindowHierarchy" not in source
        assert "takeScreenshot" not in source
    assert "fixedFailureCode(" in java
    assert "FALLBACK_ERROR_CODE" in java
    assert "failure.getMessage()" not in java
    assert "logcat" not in java


def test_configure_observation_identifies_the_selected_profile() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    copy_profiles = source.index("copyProfiles(observation, configuredResult")
    selected = source.index('if (command.has("profile_index"))', copy_profiles)
    record = source.index('observation.put("connection", selectedConnection(', selected)
    configured = source.index("configured = true;", record)

    assert copy_profiles < selected < record < configured


def test_hosted_driver_foregrounds_product_activity_before_vpn_consent() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    helper = source.index("private void ensureVpnReady()")
    absent = source.index("awaitVpnNetwork(false, NETWORK_RECOVERY_TIMEOUT_MILLIS)", helper)
    validated = source.index("awaitValidatedPhysicalNetwork();", absent)
    activity = source.index("Activity activity = ensureForegroundActivity();", validated)
    prepare = source.index("NativeVpnBridge.prepare(activity)", activity)
    launch = source.index("private Activity ensureForegroundActivity()", prepare)
    start = source.index("context.startActivity(launch)", launch)

    assert helper < absent < validated < activity < prepare < launch < start


def test_hosted_gui_driver_publishes_only_redacted_bounded_phase_markers() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    marker = source.index("private void markProgress(")
    assert '"operation"' in source[marker:]
    assert '"stage"' in source[marker:]
    assert '"state"' in source[marker:]
    assert '"sequence"' in source[marker:]
    assert "profile" not in source[marker:]
    assert "endpoint" not in source[marker:]
    assert "waitForIdleBounded" in source
    for stage in (
        '"configuration-control"',
        '"input-focus"',
        '"profile-entry"',
        '"consent-dialog"',
        '"consent-action"',
        '"connect-control"',
        '"disconnected-state"',
    ):
        assert stage in source


def test_hosted_gui_command_carries_private_progress_file() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root / "torturer/torturer_checks/hosted/android.py"
    ).read_text(encoding="utf-8")
    command = source.index('"progress_file": progress_name')
    cleanup = source.index("progress_name,", command)
    poll = source.index("def poll_ui_progress()")
    assert command < cleanup
    assert "_ANDROID_UI_PROGRESS_VALUE" in source[poll:]
    assert 'kind="ui-phase"' in source[poll:]


def test_vpn_consent_keeps_activity_result_in_the_product_task() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/main/kotlin/com/dobby/nativebridge/NativeVpnBridge.kt"
    ).read_text(encoding="utf-8")

    activity_branch = source.index("if (context is Activity)")
    activity_start = source.index(
        "context.startActivityForResult(permission, REQUEST_VPN_PERMISSION)",
        activity_branch,
    )
    application_start = source.index(
        "context.startActivity(permission.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))",
        activity_start,
    )

    assert activity_branch < activity_start < application_start


def test_vpn_consent_is_started_on_android_main_thread() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/main/kotlin/com/dobby/nativebridge/NativeVpnBridge.kt"
    ).read_text(encoding="utf-8")

    permission = source.index("if (permission != null)")
    launch = source.index("val launchConsent = Runnable", permission)
    launch_call = source.index("launchConsentOnMainThread(launchConsent)", launch)
    helper = source.index(
        "private fun launchConsentOnMainThread(launch: Runnable)", launch_call
    )
    main_check = source.index("Looper.myLooper() == Looper.getMainLooper()", launch)
    direct_run = source.index("launch.run()", main_check)
    handler = source.index("val handler = Handler(Looper.getMainLooper())", helper)
    queued_run = source.index("val posted = handler.post", handler)
    queued_catch = source.index(
        'Log.e("DobbyVPN", "Android VPN consent launch failed"', queued_run
    )
    scheduling_failure = source.index("return -1", launch_call)
    permission_result = source.index("return 0", scheduling_failure)

    assert "import android.os.Handler" in source
    assert "import android.os.Looper" in source
    assert "CountDownLatch" not in source
    assert "TimeUnit" not in source
    assert "AtomicBoolean" not in source
    assert permission < launch < launch_call < helper
    assert helper < main_check < direct_run < handler < queued_run < queued_catch
    assert launch_call < scheduling_failure < permission_result
    assert "main-thread runnable can therefore form a cycle" in source
    assert "val posted = handler.post" in source
    assert (
        "context.startActivityForResult(permission, REQUEST_VPN_PERMISSION)"
        in source[launch:handler]
    )
    assert (
        "context.startActivity(permission.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))"
        in source[launch:handler]
    )


def test_real_ui_uses_coordinate_tap_and_native_invalid_input() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/kotlin/com/dobby/GoUiInstrumentedTest.kt"
    ).read_text(encoding="utf-8")

    assert 'tapStable("Connection configuration")' in source
    assert "device.click(x, y)" in source
    assert "bounds.centerY()" in source
    focused = source.index("waitForFocusedNativeInput(10_000)")
    native_input = source.index('nativeInput.setText("invalidprofile")', focused)
    back = source.index("device.pressBack()", native_input)
    gone = source.index("waitForNativeInputGone(1_000)", back)
    connect = source.index("tapAndWaitForFailureOutcome()", gone)
    assert focused < native_input < back < connect
    assert 'device.executeShellCommand("input text invalidprofile")' not in source
    assert 'arrayOf("Error", "Failed")' in source
    assert "ACTION_SET_TEXT" in source
    assert "private fun waitForFocusedNativeInput" in source
    assert "waitForNativeInputText" not in source
    assert "waitForNativeInputCleared" not in source
    assert "private fun waitForNativeInputGone" in source
    assert "private fun waitForStableBounds" in source
    assert "private fun tapStable" in source
    assert "private fun tapAndWaitForFailureOutcome" in source
    assert "private fun tapAndWaitForVisible" in source
    assert "ANDROID_UI_TAP_FAILED" in source
    assert "ANDROID_UI_STATE_TIMEOUT" in source
    assert "TestWatcher" not in source
    assert "takeScreenshot" not in source
    assert "dumpWindowHierarchy" not in source
    assert "dobbyvpn-ui-failure" not in source
    assert "ClipboardManager" not in source
    assert "dobby.ui_profile" not in source


def test_hosted_real_ui_settles_editor_before_connect() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    assert "UI_STABILITY_SAMPLES = 10" in source
    tap_helper = source.index("private void tapUiControl(")
    assert "stable >= UI_STABILITY_SAMPLES" in source[tap_helper:]

    configure = source.index("private void configureThroughRenderedUI(")
    input_dismissed = source.index(
        'markProgress("configure", "input-dismiss", "completed")', configure
    )
    profile_entry = source.index(
        'injectProfileThroughNativeInput(text, deadline);', configure
    )
    helper = source.index("private void injectProfileThroughNativeInput(", profile_entry)
    input_settle = source.index(
        'waitForIdleBounded(uiDevice(), deadline)', helper
    )
    assert profile_entry < input_dismissed < helper < input_settle
    assert 'input.setText(text);' not in source[configure:input_dismissed]
    navigation = source.index(
        'markProgress("configure", "rendered-navigation", "started")',
        input_dismissed,
    )
    settings = source.index('"Settings"', navigation)
    back = source.index('"Back"', settings)
    returned = source.index(
        'waitForUiState(\n                "Disconnected"',
        back,
    )
    connect = source.index(
        'tapUiControl(\n                CONNECTION_ACTION_LABEL',
        source.index("private boolean connectThroughRenderedUI("),
    )
    assert input_dismissed < navigation < settings < back < returned < connect


def test_hosted_real_ui_commits_profile_through_focused_ime() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    configure = source.index("private void configureThroughRenderedUI(")
    helper = source.index("private void injectProfileThroughNativeInput(", configure)
    helper_end = source.index("private JSONObject snapshotResult", helper)
    body = source[helper:helper_end]

    assert "ClipboardManager" not in source
    assert "ClipData" not in source
    assert "instrumentation.runOnMainSync" in body
    assert "activity.getCurrentFocus()" in body
    assert "focused instanceof EditText" in body
    assert "focused.onCreateInputConnection(editorInfo)" in body
    assert "String text = new String(profile, StandardCharsets.UTF_8);" in source
    assert "String text = new String(profile, StandardCharsets.UTF_8).trim();" not in source
    assert "commitAndVerifyNativeInput(instrumentation, text, deadline)" in body
    assert "PROFILE_INPUT_CHUNK_CODE_UNITS = 4_096" in source
    assert "while (start < text.length())" in body
    assert "editor.setSelection(editor.length());" in body
    assert "String chunk = text.substring(start, end);" in body
    assert "connection.commitText(chunk, 1)" in body
    assert "if (!connection.finishComposingText())" in body
    assert "Character.isHighSurrogate(text.charAt(end - 1))" in body
    assert "Character.isLowSurrogate(text.charAt(end))" in body
    assert body.count("System.currentTimeMillis() >= deadline") >= 2
    assert '"ANDROID_UI_INPUT_COMMIT_FAILED"' in body
    assert '"ANDROID_UI_INPUT_FOCUS_LOST"' in body
    assert "waitForIdleBounded(uiDevice(), deadline)" in body
    assert "String[] lines = text.split(\"\\n\", -1);" not in body
    assert "instrumentation.sendKeySync" not in body
    assert "KeyEvent" not in source
    assert "AccessibilityNodeInfo.ACTION_PASTE" not in body
    assert "KeyEvent.KEYCODE_V" not in body
    assert "pressKeyCode(" not in body
    assert "ANDROID_UI_PASTE_INCOMPLETE" not in body
    assert "input.setText(text);" not in body
    assert "input.setText(text);" not in source
    assert "nativeEditorBufferMatches" not in body
    assert "getText()" not in body
    for diagnostic in (
        "ANDROID_UI_INPUT_REJECTED",
        "ANDROID_UI_INPUT_FIRST_CHUNK_ONLY",
        "ANDROID_UI_INPUT_TRUNCATED",
        "ANDROID_UI_INPUT_TRANSFORMED",
        "ANDROID_UI_INPUT_INCOMPLETE",
    ):
        assert diagnostic not in body


def test_hosted_real_ui_refreshes_activity_before_native_input() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    helper = source.index("private void injectProfileThroughNativeInput(")
    refresh = source.index("foregroundActivity = ensureForegroundActivity();", helper)
    assert refresh > helper
    assert "input.isFocused()" not in source[helper:source.index(
        "private void commitAndVerifyNativeInput", helper
    )]
    assert "currently resumed Activity" in source[helper:refresh]


def test_hosted_real_ui_fails_fast_on_rendered_connect_error() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    connect = source.index("private boolean connectThroughRenderedUI(")
    body_end = source.index("private void disconnectThroughRenderedUI", connect)
    body = source[connect:body_end]
    error_state = body.index('"Error".equals(postTapState)')
    category = body.index("postTapErrorCategory = visibleErrorCategory(", error_state)
    progress = body.index("progressPostTapState = postTapState", category)
    fail_fast = body.index('"ANDROID_UI_CONNECT_FAILED"', progress)
    consent = body.index("if (consentNeeded)", fail_fast)

    assert error_state < category < progress < fail_fast < consent
    assert "postTapErrorCategory" in body[fail_fast:consent]
    assert "postTapState" not in body[fail_fast:consent]
    assert "knownNonPermissionFailure" in body
    assert "unclassifiedWithoutConsent" in body
    assert '"MALFORMED_CONFIG"' in source
    assert '"UNCLASSIFIED"' in source


def test_hosted_real_ui_allows_unclassified_error_through_pending_consent() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    connect = source.index("private boolean connectThroughRenderedUI(")
    body_end = source.index("private void disconnectThroughRenderedUI", connect)
    body = source[connect:body_end]
    known = body.index("boolean knownNonPermissionFailure")
    unknown = body.index("boolean unclassifiedWithoutConsent", known)
    fail_fast = body.index('"ANDROID_UI_CONNECT_FAILED"', unknown)
    consent = body.index("if (consentNeeded)", fail_fast)
    category_helper = source.index(
        "private boolean isKnownNonPermissionErrorCategory(String category)"
    )
    category_helper_end = source.index(
        "private UiObject2 waitForFocusedNativeInput", category_helper
    )
    category_helper_body = source[category_helper:category_helper_end]

    assert "isKnownNonPermissionErrorCategory(" in body[known:fail_fast]
    assert "postTapErrorCategory" in body[known:fail_fast]
    assert '!consentNeeded' in body[unknown:fail_fast]
    assert '"UNCLASSIFIED".equals(postTapErrorCategory)' in body[unknown:fail_fast]
    assert known < unknown < fail_fast < consent
    assert '"PLATFORM_PERMISSION_REQUIRED"' not in category_helper_body
    assert '"PLATFORM_PERMISSION_REQUIRED"' in source


def test_hosted_real_ui_polls_redacted_error_category_with_connect_deadline() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    connect = source.index("private boolean connectThroughRenderedUI(")
    body_end = source.index("private void disconnectThroughRenderedUI", connect)
    body = source[connect:body_end]
    category = body.index("postTapErrorCategory = visibleErrorCategory(")
    category_end = body.index("postTapState +=", category)
    category_call = body[category:category_end]
    helper = source.index("private String visibleErrorCategory(long timeout)")
    helper_end = source.index("private UiObject2 waitForFocusedNativeInput", helper)
    helper_body = source[helper:helper_end]

    assert "ERROR_CATEGORY_TIMEOUT_MILLIS" in category_call
    assert "ERROR_CATEGORY_TIMEOUT_MILLIS = 2_000L" in source
    assert "remainingTimeout(deadline" in category_call
    assert "Math.min(" in category_call
    assert "long deadline = System.currentTimeMillis()" in helper_body
    assert "while (System.currentTimeMillis() < deadline)" in helper_body
    assert "Thread.sleep(POLL_MILLIS)" in helper_body
    assert '"MALFORMED_CONFIG"' in helper_body
    assert '"UNCLASSIFIED"' in helper_body
    assert "getText()" not in helper_body
    assert "getContentDescription()" not in helper_body


def test_hosted_real_ui_classifies_connected_state_error_after_retry() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    wait = source.index("private void waitForUiState(")
    wait_end = source.index("private String awaitVisibleConnectionState", wait)
    body = source[wait:wait_end]
    error_state = body.index('"Connected".equals(expected)')
    category = body.index("String category = visibleErrorCategory(", error_state)
    deadline = body.index('remainingTimeout(\n                                        deadline, "ANDROID_UI_CONNECT_TIMEOUT")', category)
    failure = body.index('"ANDROID_UI_CONNECT_FAILED"', deadline)

    assert error_state < category < deadline < failure
    assert "ERROR_CATEGORY_TIMEOUT_MILLIS" in body[category:failure]
    assert "Math.min(" in body[category:failure]
    assert "getText()" not in body
    assert "getContentDescription()" not in body

    connect = source.index("private boolean connectThroughRenderedUI(")
    connect_end = source.index("private void disconnectThroughRenderedUI", connect)
    connect_body = source[connect:connect_end]
    retry = connect_body.index('markProgress(operation, "connect-retry", "started")')
    connected = connect_body.index('markProgress(operation, "connected-state", "started")')
    assert retry < connected


def test_hosted_driver_waits_for_the_vpn_grant_after_clicking_consent() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    consent = source.index("acceptVpnConsent();")
    second_prepare = source.index("result = NativeVpnBridge.prepare(activity);", consent)
    helper = source.index("private void acceptVpnConsent()", second_prepare)
    click = source.index("button.click();", helper)
    granted = source.index("VpnService.prepare(context) == null", click)

    assert consent < second_prepare < helper < click < granted


def test_hosted_driver_does_not_gate_vpn_consent_on_accessibility_clickable() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    helper = source.index("private androidx.test.uiautomator.UiObject2 findVpnConsentButton")
    helper_end = source.index("private Network awaitVpnNetwork", helper)
    body = source[helper:helper_end]

    assert '"android:id/button1"' in body
    assert '"com.android.vpndialogs:id/button1"' in body
    assert '"OK"' in body
    assert "button.isEnabled()" in body
    assert "button.isClickable()" not in body
    assert "button.click();" in source[source.index("private void acceptVpnConsent"):]


def test_hosted_driver_emits_only_fixed_consent_timeout_diagnostics() -> None:
    root = Path(__file__).resolve().parents[2]
    java_source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")
    diagnostic = java_source.index("private void markConsentTimeoutDiagnostic")
    diagnostic_end = java_source.index("private Network awaitVpnNetwork", diagnostic)
    body = java_source[diagnostic:diagnostic_end]

    assert '"consent-diagnosis"' in body
    assert '"consent_diagnostic"' in body
    assert '"foreground"' in body
    assert '"button1"' in body
    assert '"vpn_permission"' in body
    assert '"launch_state"' in body
    assert '"post_tap_state"' in body
    assert "NativeVpnBridge.consentLaunchStateForTest()" in body
    assert "private String postTapState()" in body
    for value in ("VPN_DIALOG", "PRODUCT", "OTHER", "NONE"):
        assert f'"{value}"' in body
    for value in (
        "Connecting",
        "Connected",
        "Error",
        "Failed",
        "Ready",
        "Disconnected",
        "UNKNOWN",
    ):
        assert f'"{value}"' in body
    assert 'By.res("android:id/button1")' in body
    assert "VpnService.prepare(context)" in body
    assert "getText()" not in body
    assert "getClassName()" not in body
    assert "getContentDescription()" not in body

    bridge_source = (
        root
        / "android_module/app/src/main/kotlin/com/dobby/nativebridge/NativeVpnBridge.kt"
    ).read_text(encoding="utf-8")
    assert "consentLaunchStateForTest" in bridge_source
    for value in ("NOT_REQUESTED", "QUEUED", "STARTED", "RETURNED", "FAILED"):
        assert f'"{value}"' in bridge_source

    adapter_source = (
        root / "torturer/torturer_checks/hosted/android.py"
    ).read_text(encoding="utf-8")
    assert "_ANDROID_UI_CONSENT_DIAGNOSTIC_VALUES" in adapter_source
    assert '"launch_state"' in adapter_source
    assert '"post_tap_state"' in adapter_source
    assert "set(consent_diagnostic) != set(" in adapter_source
    assert "consent_diagnostic=diagnostic_values" in adapter_source
    poll_start = adapter_source.index("def poll_ui_progress()")
    check_worker = adapter_source.index("def check_worker()", poll_start)
    poll_body = adapter_source[poll_start:check_worker]
    assert "if progress_name is None:" in poll_body
    assert "not worker.is_alive()" not in poll_body


def test_hosted_driver_uses_the_go_session_start_mode_value() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    assert 'START_MODE_PROFILE_INDEX = "PROFILE_INDEX"' in source
    assert source.count(
        "sessionID, sequence, START_MODE_PROFILE_INDEX, index"
    ) == 2
    assert 'sessionID, sequence, "profile_index", index' not in source


def test_hosted_driver_reconnects_from_the_post_stop_sequence() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    stop = source.index("NativeGoSession.stop(sessionID, generation)")
    idle = source.index('awaitState(sessionID, "IDLE"', stop)
    sequence = source.index('sequence = idle.optLong("sequence", sequence);', idle)
    removed = source.index("awaitVpnNetwork(", sequence)
    removed_state = source.index("false, operationTimeout(operation)) == null", removed)
    reconnect = source.index("case \"reconnect\"", sequence)
    restart = source.index(
        "sessionID, sequence, START_MODE_PROFILE_INDEX, index", reconnect
    )
    connected = source.index('awaitState(sessionID, "CONNECTED"', restart)
    fresh_vpn = source.index("awaitVpnNetwork(true, operationTimeout(operation))", connected)

    assert stop < idle < sequence < removed < removed_state < reconnect < restart < fresh_vpn


def test_hosted_driver_records_second_tunnel_and_routing_facts_by_step_id() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    operation_id = source.index('String operationID = operation.getString("id");')
    second_tunnel = source.index('"second-tunnel".equals(operationID)', operation_id)
    second_tunnel_fact = source.index('"second_tunnel_interface"', second_tunnel)
    second_routing = source.index('"second-routing".equals(operationID)', second_tunnel_fact)
    second_routing_fact = source.index('"second_routing_verified"', second_routing)

    assert operation_id < second_tunnel < second_tunnel_fact < second_routing < second_routing_fact


def test_hosted_driver_keeps_network_probe_identity_without_failure_details() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    request = source.index("private JSONObject networkRequest(")
    failure = source.index("private JSONObject networkRequestFailure(", request)
    assert source.index("network.openConnection(url)", request) < failure
    assert source.index('setInstanceFollowRedirects(false)', request) < failure
    assert source.index('setRequestProperty("User-Agent", "DobbyVPN-Harness/1")', request) < failure
    assert source.index('setRequestProperty("Connection", "close")', request) < failure
    assert source.index("getErrorStream()", request) < failure
    assert source.index('.put("status", status)', request) < failure
    assert source.index('.put("body", body)', request) < failure
    assert "private String sanitizedFailure(" not in source
    assert "private String sanitizedOutput(" not in source
    assert 'return new JSONObject().put("error_code", "ANDROID_NETWORK_REQUEST_FAILED")' in source
    assert 'throw new IOException("ANDROID_NETWORK_REQUEST_FAILED", failure)' in source
    failure_end = source.index("private JSONObject shellNetworkRequest", failure)
    for field in ('"network_id"', '"network_binding"', '"host"', '"port"',
                  '"error_type"', '"error"', '"stack"'):
        assert field not in source[failure:failure_end]


def test_hosted_driver_keeps_direct_blocked_and_provider_vpn_requests() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    routing = source.index("private void runRoutingProof(")
    resolved = source.index(
        "JSONArray identityAddresses = resolveIdentityIpv4s(physical, identity.getHost())",
        routing,
    )
    announced = source.index('.put("ipv4s", identityAddresses)', resolved)
    direct = source.index('"direct", networkRequest(', announced)
    direct_network = source.index(
        "phasePhysical, identity.toString(), directRequired", direct
    )
    vpn = source.index('"vpn", routingProviderRequest(', direct_network)
    vpn_network = source.index("identity.toString()", vpn)
    provider = source.index("private JSONObject routingProviderRequest(", vpn_network)
    call = source.index("ContentResolver resolver = testContext.getContentResolver();", provider)
    helper = source.index("private JSONObject networkRequest(", call)
    working_network = source.index("network.openConnection(url)", helper)

    gradle = (root / "android_module/app/build.gradle.kts").read_text(encoding="utf-8")
    assert "okhttp" not in gradle.lower()
    assert "pinnedNetworkRequest" not in source
    assert "OkHttp" not in source
    assert resolved < announced < direct < direct_network < vpn < vpn_network < provider < call
    assert helper < working_network
    assert "networkRequest(null" not in source


def test_hosted_driver_positive_routing_probe_uses_test_provider_uid() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    routing = source.index("private JSONObject routingProviderRequest(")
    call = source.index("resolver.call(", routing)
    test_uid = source.index("int testUid = testContext.getApplicationInfo().uid;", call)
    target_uid = source.index("int targetUid = context.getApplicationInfo().uid;", test_uid)
    identity = source.index("probeUid != testUid || probeUid == targetUid", target_uid)
    next_method = source.index("private void runNetworkTransition(", identity)
    assert routing < call < test_uid < target_uid < identity < next_method
    assert "routingDefaultRequest" not in source
    assert "networkRequest(null" not in source
    assert "shellNetworkRequest(" not in source[routing:next_method]


def test_hosted_driver_uses_shell_uid_for_stability_and_throughput() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    stability = source.index("private void measureStability(")
    throughput = source.index("private JSONObject measureThroughput(", stability)
    assert source.index('requiredShellNetworkRequest("get", endpoint, 0)', stability) < throughput
    assert source.index(
        'requiredShellNetworkRequest("get", download, 0)', throughput
    ) < source.index(
        '"upload", upload, 64 * 1024', throughput
    )


def test_hosted_driver_requires_successful_http_stability_samples() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    stability = source.index("private void measureStability(")
    throughput = source.index("private JSONObject measureThroughput(", stability)
    sample = source.index(
        'JSONObject sample = requiredShellNetworkRequest("get", endpoint, 0);',
        stability,
    )
    status = source.index('sample.optInt("status", 0)', sample)
    success = source.index("status < 200 || status >= 300", status)
    failure = source.index('"ANDROID_STABILITY_HTTP_STATUS"', success)

    assert stability < sample < status < success < failure < throughput


def test_hosted_driver_does_not_repeat_stale_vpn_lookup_before_metrics() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    stability = source.index("private void measureStability(")
    throughput = source.index("private JSONObject measureThroughput(", stability)
    next_method = source.index("private long operationTimeout(", throughput)
    metric_source = source[stability:next_method]

    assert "requiredShellNetworkRequest(\"get\", endpoint, 0)" in metric_source
    assert "requiredShellNetworkRequest(\"get\", download, 0)" in metric_source
    assert "findNetwork(NetworkCapabilities.TRANSPORT_VPN)" not in metric_source
    assert "ANDROID_VPN_NETWORK_UNAVAILABLE" not in metric_source


def test_hosted_driver_uses_one_bounded_provider_routing_request() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    assert '.put("vpn", routingProviderRequest(identity.toString()))' in source
    assert "ROUTING_REQUEST_ATTEMPTS" not in source
    assert "private JSONObject routingDefaultRequest(" not in source
    helper = source.index("private JSONObject routingProviderRequest(")
    helper_end = source.index("private void runNetworkTransition(", helper)
    helper_body = source[helper:helper_end]
    assert "resolver.call(" in helper_body
    assert "Thread.sleep" not in helper_body


def test_hosted_driver_collects_routing_counters_before_surfacing_provider_failure() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (root / "torturer/torturer_checks/hosted/android.py").read_text(
        encoding="utf-8"
    )
    proof = source.index("def _routing_proof(")
    blocked = source.index("blocked = self._routing_ready(", proof)
    semantic = source.index("tunneled_ip = self._assert_routing_blocked(blocked)", blocked)
    counters = source.index("after_rx, after_tx = self._routing_counters(vpn, deadline)", semantic)
    rule = source.index("packets = self._routing_rule_counter(", counters)
    finish = source.index("cleanup_deadline = max(", rule)
    assert blocked < semantic < counters < rule < finish
    assert "android_routing_tun_rx_delta=" in source[semantic:finish]
    assert "android_routing_tun_tx_delta=" in source[semantic:finish]
    assert "android_routing_rule_packets=" in source[semantic:finish]


def test_hosted_driver_waits_for_restored_physical_network_validation() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    routing = source.index("private void runRoutingProof(")
    awaited = source.index("Network physical = awaitValidatedPhysicalNetwork();", routing)
    helper = source.index("private Network awaitValidatedPhysicalNetwork()", awaited)
    not_vpn = source.index("NetworkCapabilities.NET_CAPABILITY_NOT_VPN", awaited, helper)
    internet = source.index("NetworkCapabilities.NET_CAPABILITY_INTERNET", helper)
    validated = source.index("NetworkCapabilities.NET_CAPABILITY_VALIDATED", internet)
    stable = source.index("if (stableSamples >= 2) return candidate;", validated)

    assert routing < awaited < not_vpn < helper < internet < validated < stable
    assert 'ANDROID_PHYSICAL_NETWORK_NOT_VALIDATED' in source


def test_hosted_driver_declares_a_private_test_apk_routing_provider() -> None:
    root = Path(__file__).resolve().parents[2]
    manifest = (root / "android_module/app/src/androidTest/AndroidManifest.xml").read_text(
        encoding="utf-8"
    )
    driver = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")
    provider = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiRoutingProbeProvider.java"
    ).read_text(encoding="utf-8")
    shell_probe = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiNetworkProbeMain.java"
    ).read_text(encoding="utf-8")

    assert 'android.permission.INTERNET' in manifest
    assert 'android.permission.ACCESS_NETWORK_STATE' in manifest
    assert 'android:name="com.dobby.GoUiRoutingProbeProvider"' in manifest
    assert 'android:authorities="com.dobby.vpn.test.routing_probe"' in manifest
    assert 'android:process=":routing_probe"' in manifest
    assert 'android:exported="true"' in manifest
    assert "GoUiNetworkProbeService" not in manifest
    assert "GoUiNetworkProbeMain.class" in driver
    assert "NETWORK_PROBE_CLASS.getName()" in driver
    assert "getContext().getApplicationInfo()" in driver
    assert "probeApplication.splitSourceDirs" not in driver
    assert 'entry.getName().matches("classes[0-9]*\\\\.dex")' in driver
    assert "String.join(File.pathSeparator, dexPaths)" in driver
    assert "ANDROID_NETWORK_PROBE_DEX_MISSING" in driver
    assert "ANDROID_NETWORK_PROBE_CLASS_DEX_MISSING" in driver
    assert '"grep -a -l GoUiNetworkProbeMain"' in driver
    assert "executeShellCommandRwe(" in driver
    assert '"dd of=" + path + " bs=65536"' in driver
    assert 'automationShell(automation, "stat -c %s " + path)' in driver
    assert "ANDROID_NETWORK_PROBE_STAGE_SIZE_INVALID" in driver
    assert "ParcelFileDescriptor.AutoCloseOutputStream" in driver
    assert "Build.VERSION.SDK_INT < 34" in driver
    assert 'automationShell(automation, "chmod 0700 " + probeRoot)' in driver
    assert 'automationShell(automation, "chmod 0755 " + probeRoot)' in driver
    assert "endpoint.openConnection()" in shell_probe
    assert "MAX_BODY_TEXT_BYTES" in shell_probe
    assert "app_process -cp" in driver
    assert 'device.executeShellCommand("su 2000 id -u")' in driver
    assert '"2000".equals(shellUid)' in driver
    assert 'String command = "su 2000 app_process -cp "' in driver
    assert "Base64.getUrlEncoder().withoutPadding()" in driver
    assert "Base64.getUrlDecoder().decode(arguments[1])" in shell_probe
    assert "shellQuote(" not in driver
    assert "/data/local/tmp/dobbyvpn-probe-" in driver
    assert 'String outputPath = probeRoot + "/result.json"' in driver
    assert 'device.executeShellCommand("cat " + outputPath)' in driver
    assert 'device.executeShellCommand("rm -rf " + probeRoot)' in driver
    assert "writeResult(outputPath, result)" in shell_probe
    assert "System.out.println(result.toString())" in shell_probe
    assert "logcat" not in driver
    assert "ANDROID_NETWORK_PROBE_OUTPUT_INVALID" in driver
    assert "reportedUid != 2000" in driver
    assert "reportedUid == context.getApplicationInfo().uid" in driver
    assert "ContentResolver" in driver
    assert "resolver.call(" in driver
    assert "testContext.getApplicationInfo().uid" in driver
    assert "context.getApplicationInfo().uid" in driver
    assert "probeUid != testUid || probeUid == targetUid" in driver
    assert '"default".equals(binding)' in driver
    assert "GoUiRoutingProbeProvider.AUTHORITY" in driver
    assert "GoUiRoutingProbeProvider.METHOD_PROBE" in driver
    assert "ANDROID_NETWORK_PROBE_PROVIDER_ACCESS_DENIED" in driver
    assert "ANDROID_NETWORK_PROBE_PROVIDER_FAILED" in driver
    assert "ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID" in driver
    assert "ANDROID_NETWORK_PROBE_PROVIDER_IDENTITY_INVALID" in driver
    assert "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN" in driver
    assert "extends ContentProvider" in provider
    assert "HttpsURLConnection" in provider
    assert "Process.myUid()" in provider
    assert "endpoint.openConnection()" in provider
    assert "Network.openConnection" not in provider
    assert "setConnectTimeout(CONNECT_TIMEOUT_MILLIS)" in provider
    assert "setReadTimeout(READ_TIMEOUT_MILLIS)" in provider
    assert "readBody(response, bodyDeadline)" in provider
    assert "MAX_BODY_BYTES" not in provider
    assert '"https".equalsIgnoreCase(endpoint.getProtocol())' in provider
    assert "endpoint.getQuery() != null" in provider
    assert "containsWhitespace(value)" in provider
    assert '"network_binding"' in provider
    assert '"network_transport"' in provider
    assert '"probe_uid"' in provider
    assert '"status"' in provider
    assert '"body"' in provider
    assert '"error_code"' in provider
    assert "ANDROID_NETWORK_REQUEST_FAILED" in provider
    assert "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN" in provider
    assert "ConnectivityManager" in provider
    assert "getActiveNetwork()" in provider
    assert "NetworkCapabilities.TRANSPORT_VPN" in provider
    assert "NETWORK_TRANSPORT_NON_VPN" in provider
    assert "Thread.sleep" not in provider
    assert "bindProcessToNetwork" not in provider


def test_hosted_driver_waits_for_provider_vpn_before_publishing_routing_ready() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    routing = source.index("private void runRoutingProof(")
    ready_probe = source.index(
        "JSONObject providerReady = awaitProviderDefaultVpn(deadlineElapsedRealtime);",
        routing,
    )
    ready = source.index("JSONObject ready = new JSONObject()", ready_probe)
    write = source.index("writeJson(new File(control.getPath() + \".ready\"), ready);", ready)
    provider_fields = source.index('.put("provider_uid"', ready)
    provider_transport = source.index('.put("provider_network_transport"', provider_fields)

    assert routing < ready_probe < ready < provider_fields < provider_transport < write
    assert "deadlineElapsedRealtime" in source[routing:ready]
    assert '"provider_network_binding"' in source[ready:write]

    observe = source.index('case "observe_routing_identity":')
    observe_call = source.index("runRoutingProof(", observe)
    observe_deadline = source.index(
        "SystemClock.elapsedRealtime() + operationTimeout(operation)", observe_call
    )
    transition = source.index('case "network_transition":')
    transition_call = source.index("runNetworkTransition(", transition)
    transition_deadline = source.index(
        "SystemClock.elapsedRealtime() + operationTimeout(operation)", transition_call
    )
    assert observe < observe_call < observe_deadline
    assert transition < transition_call < transition_deadline


def test_routing_provider_waits_for_default_vpn_with_bounded_callback_race_closure() -> None:
    root = Path(__file__).resolve().parents[2]
    provider = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiRoutingProbeProvider.java"
    ).read_text(encoding="utf-8")

    method = provider.index("private Bundle awaitDefaultVpn(")
    initial = provider.index("Network current = connectivity.getActiveNetwork();", method)
    initial_check = provider.index("isDefaultVpn(connectivity, current)", initial)
    register = provider.index("connectivity.registerDefaultNetworkCallback(callback);", initial_check)
    reread = provider.index("Network afterRegistration = connectivity.getActiveNetwork();", register)
    signal = provider.index("signalIfDefaultVpn(connectivity, afterRegistration", reread)
    await_call = provider.index("ready.await(remainingMillis, TimeUnit.MILLISECONDS)", signal)
    finally_block = provider.index("} finally {", await_call)
    unregister = provider.index("connectivity.unregisterNetworkCallback(callback);", finally_block)

    assert method < initial < initial_check < register < reread < signal < await_call
    assert await_call < finally_block < unregister
    assert "SystemClock.elapsedRealtime()" in provider[method:finally_block]
    assert "KEY_DEADLINE_ELAPSED_REALTIME" in provider[method:finally_block]
    assert "onAvailable(Network network)" in provider[method:register]
    assert "onCapabilitiesChanged(" in provider[method:register]
    assert "CountDownLatch" in provider[method:finally_block]
    assert "AtomicReference<Network>" in provider[method:finally_block]
    assert "Thread.sleep" not in provider[method:finally_block]
    assert "NetworkRequest" not in provider[method:finally_block]
    assert "openConnection" not in provider[method:finally_block]
    assert "bindProcessToNetwork" not in provider[method:finally_block]
