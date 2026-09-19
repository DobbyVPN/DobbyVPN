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
    start = source.index(".startActivitySync(launch)", launch)

    assert helper < absent < validated < activity < prepare < launch < start


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
    text = source.index("waitForNativeInputText(\"invalidprofile\", 10_000)", native_input)
    cleared = source.index('nativeInput.setText("")', text)
    clear_wait = source.index("waitForNativeInputCleared(10_000)", cleared)
    back = source.index("device.pressBack()", clear_wait)
    gone = source.index("waitForNativeInputGone(1_000)", back)
    connect = source.index("tapAndWaitForFailureOutcome()", gone)
    assert focused < native_input < text < cleared < clear_wait < back < connect
    assert 'device.executeShellCommand("input text invalidprofile")' not in source
    assert 'arrayOf("Error", "Failed")' in source
    assert "private fun waitForFocusedNativeInput" in source
    assert "private fun waitForNativeInputText" in source
    assert "private fun waitForNativeInputCleared" in source
    assert "private fun waitForNativeInputGone" in source
    assert "private fun waitForStableBounds" in source
    assert "private fun tapStable" in source
    assert "private fun tapAndWaitForFailureOutcome" in source
    assert "private fun tapAndWaitForVisible" in source
    assert "val failureDiagnostics = object : TestWatcher()" in source
    assert "override fun failed(" in source
    assert "Android touch injection failed" in source
    assert "ClipboardManager" not in source
    assert "dobby.ui_profile" not in source


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


def test_hosted_driver_keeps_network_probe_identity_and_failure_details() -> None:
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
    assert source.index(
        '.put("network_id", network.getNetworkHandle())', failure
    ) < source.index("private String sanitizedFailure(", failure)
    assert source.index('"ANDROID_NETWORK_REQUEST_FAILED:', request) < source.index(
        "private JSONObject networkRequestFailure(", request
    )


def test_hosted_driver_keeps_direct_blocked_and_vpn_required_requests() -> None:
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
    vpn = source.index('"vpn", routingVpnRequest(', direct_network)
    vpn_network = source.index("identity.toString()", vpn)
    helper = source.index("private JSONObject networkRequest(", vpn_network)
    working_network = source.index("network.openConnection(url)", helper)

    gradle = (root / "android_module/app/build.gradle.kts").read_text(encoding="utf-8")
    assert "okhttp" not in gradle.lower()
    assert "pinnedNetworkRequest" not in source
    assert "OkHttp" not in source
    assert resolved < announced < direct < direct_network < vpn < vpn_network
    assert helper < working_network


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


def test_hosted_driver_bounds_routing_request_startup_retries() -> None:
    root = Path(__file__).resolve().parents[2]
    source = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")

    assert "private static final int ROUTING_REQUEST_ATTEMPTS = 3;" in source
    assert '.put("vpn", routingVpnRequest(identity.toString()))' in source
    assert "attempt < ROUTING_REQUEST_ATTEMPTS" in source
    assert 'if (!latest.has("error_type")) return latest;' in source


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


def test_hosted_driver_does_not_add_an_exported_network_probe_service() -> None:
    root = Path(__file__).resolve().parents[2]
    manifest = (root / "android_module/app/src/androidTest/AndroidManifest.xml").read_text(
        encoding="utf-8"
    )
    driver = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
    ).read_text(encoding="utf-8")
    probe = (
        root
        / "android_module/app/src/androidTest/java/com/dobby/GoUiNetworkProbeMain.java"
    ).read_text(encoding="utf-8")

    assert "GoUiNetworkProbeService" not in manifest
    assert 'android:exported="true"' not in manifest
    assert "GoUiNetworkProbeService" not in driver
    assert "bindService(" not in driver
    assert "import android.app.UiAutomation" not in probe
    assert "Process.myUid()" in probe
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
    assert "endpoint.openConnection()" in probe
    assert "MAX_BODY_TEXT_BYTES" in probe
    assert "app_process -cp" in driver
    assert 'device.executeShellCommand("su 2000 id -u")' in driver
    assert '"2000".equals(shellUid)' in driver
    assert 'String command = "su 2000 app_process -cp "' in driver
    assert "Base64.getUrlEncoder().withoutPadding()" in driver
    assert "Base64.getUrlDecoder().decode(arguments[1])" in probe
    assert "shellQuote(" not in driver
    assert "/data/local/tmp/dobbyvpn-probe-" in driver
    assert 'String outputPath = probeRoot + "/result.json"' in driver
    assert 'device.executeShellCommand("cat " + outputPath)' in driver
    assert 'device.executeShellCommand("rm -rf " + probeRoot)' in driver
    assert "writeResult(outputPath, result)" in probe
    assert "System.out.println(result.toString())" in probe
    assert '"logcat -d -t 100 -v brief -s appproc AndroidRuntime System.err"' in driver
    assert "ANDROID_NETWORK_PROBE_OUTPUT_INVALID" in driver
    assert "reportedUid != 2000" in driver
    assert "reportedUid == context.getApplicationInfo().uid" in driver
