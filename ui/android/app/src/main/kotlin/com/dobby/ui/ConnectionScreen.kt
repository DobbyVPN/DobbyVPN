package com.dobby.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.windowInsetsBottomHeight
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.testTagsAsResourceId
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog

internal val Paper = Color(0xFFD9D2C9)
internal val Ink = Color(0xFF141414)
internal val Surface = Color(0xFFF6F3EE)
internal val Muted = Color(0xFF55504A)
private val Rule = Color(0xFFB9B1A6)
private val Tray = Color(0xFFCCC4B9)

private val ButtonShape = RoundedCornerShape(10.dp)
private val MaxTrayHeight = 264.dp
private val MinTrayHeight = 72.dp
private val ControlsHeight = 180.dp

@Composable
internal fun DobbyApp(controller: SessionController) {
    val focus = LocalFocusManager.current
    MaterialTheme(colorScheme = lightColorScheme(primary = Ink, background = Paper)) {
        BoxWithConstraints(
            Modifier.fillMaxSize()
                // Takes focus when the field gives it up, so the keyboard does not come back.
                .focusable()
                // A tap outside the subscription field hides the keyboard; buttons and the field handle their own taps.
                .pointerInput(Unit) { detectTapGestures { focus.clearFocus() } }
                .background(Paper)
                .semantics { testTagsAsResourceId = true },
        ) {
            // Four connections when there is room; on short screens the logs keep a quarter of the height.
            val trayHeight = (maxHeight - ControlsHeight - maxHeight / 4).coerceIn(MinTrayHeight, MaxTrayHeight)
            Column(Modifier.fillMaxSize()) {
                Column(
                    Modifier.windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal))
                        .padding(start = 12.dp, end = 12.dp, top = 8.dp, bottom = 12.dp)
                        .testTag("connection.controls")
                        .semantics { contentDescription = MainActivity.CONNECTION_CONTROLS_DESCRIPTION },
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    SubscriptionField(controller)
                    MainButton(controller)
                    Connections(controller, Modifier.padding(top = 8.dp).height(trayHeight))
                }
                Logs(controller, Modifier.weight(1f))
            }
        }
        ErrorDialog(controller)
    }
}

@Composable
private fun SubscriptionField(controller: SessionController) {
    val state = controller.state
    val focus = LocalFocusManager.current
    val showPaste = state.source.isEmpty() && !state.loading && state.canPaste
    BasicTextField(
        value = state.source,
        onValueChange = { controller.sourceChanged(it) },
        modifier = Modifier.fillMaxWidth()
            .testTag("subscription.url")
            .semantics { contentDescription = "Connection configuration" },
        singleLine = true,
        textStyle = TextStyle(color = Ink, fontSize = 15.sp),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done),
        keyboardActions = KeyboardActions(onDone = { focus.clearFocus() }),
        decorationBox = { input ->
            Row(
                Modifier.height(44.dp)
                    .clip(RoundedCornerShape(8.dp))
                    .background(Surface)
                    .border(1.5.dp, Ink, RoundedCornerShape(8.dp))
                    .padding(start = 12.dp, end = if (showPaste) 5.dp else 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Box(Modifier.weight(1f)) {
                    if (state.source.isEmpty()) Text("Subscription URL", color = Muted, fontSize = 15.sp, maxLines = 1)
                    input()
                }
                if (state.loading) {
                    Text("Loading…", color = Muted, fontSize = 13.sp)
                } else if (showPaste) {
                    Box(
                        Modifier.height(34.dp)
                            .clip(RoundedCornerShape(6.dp))
                            .background(Ink)
                            .clickable(role = Role.Button) { focus.clearFocus(); controller.paste() }
                            .testTag("subscription.paste")
                            .padding(horizontal = 14.dp),
                        contentAlignment = Alignment.Center,
                    ) { Text("Paste", color = Surface, fontSize = 14.sp, fontWeight = FontWeight.SemiBold) }
                }
            }
        },
    )
}

@Composable
private fun MainButton(controller: SessionController) {
    val title = controller.mainActionTitle
    val enabled = controller.mainActionEnabled
    val status = controller.status
    val idle = title == "Auto connect"
    val filled = enabled && idle
    val outlined = enabled || !idle
    val style = when {
        filled -> Modifier.background(Ink)
        outlined -> Modifier.border(2.dp, Ink, ButtonShape)
        else -> Modifier.border(1.5.dp, Rule, ButtonShape)
    }
    Box(
        Modifier.fillMaxWidth()
            .height(60.dp)
            .clip(ButtonShape)
            .then(style)
            .clickable(enabled = enabled, role = Role.Button, onClick = controller::mainAction)
            .testTag("connection.action")
            .semantics {
                contentDescription = "VPN connection action"
                stateDescription = status
                liveRegion = LiveRegionMode.Polite
            },
        contentAlignment = Alignment.Center,
    ) {
        Text(
            title,
            color = if (filled) Surface else if (outlined) Ink else Muted,
            fontSize = 20.sp,
            fontWeight = FontWeight.SemiBold,
        )
    }
}

@Composable
private fun Connections(controller: SessionController, modifier: Modifier) {
    val session = controller.state.session
    Box(modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Tray)) {
        val orphan = controller.orphanProfile
        if (session.profiles.isEmpty() && orphan == null && controller.state.loadError.isNotEmpty()) {
            Column(
                Modifier.align(Alignment.Center),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                SockIcon(holey = true, width = 40.dp)
                Text("Couldn't load the subscription", color = Ink, fontSize = 15.sp, fontWeight = FontWeight.Medium)
                Box(
                    Modifier.heightIn(min = 40.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(Ink)
                        .clickable(role = Role.Button, onClick = controller::retryLoad)
                        .testTag("tray.retry")
                        .padding(horizontal = 22.dp),
                    contentAlignment = Alignment.Center,
                ) { Text("Retry", color = Surface, fontSize = 15.sp, fontWeight = FontWeight.SemiBold) }
            }
        } else if (session.profiles.isEmpty() && orphan == null) {
            SockIcon(
                holey = false,
                width = 40.dp,
                modifier = Modifier.align(Alignment.Center).semantics { contentDescription = "No subscription yet" },
            )
        } else {
            LazyColumn(
                Modifier.fillMaxSize(),
                contentPadding = PaddingValues(8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                if (orphan != null) item(key = "active") { OrphanRow(controller, orphan) }
                items(session.profiles, key = { it.index }) { ConnectionRow(controller, it, session.digest) }
            }
        }
    }
}

@Composable
private fun ConnectionRow(controller: SessionController, profile: ProfileData, digest: String) {
    val status = controller.profileStatus(profile.index)
    ConnectionButton(
        name = profile.name,
        details = listOfNotNull(protocolName(profile.protocol), status?.label),
        status = status,
        action = controller.actionTitle(profile.index).let {
            if (status == ProfileStatus.CONNECTING || it == "Stop") "Connecting…" else it
        },
        enabled = controller.canAct(profile.index),
        onClick = { controller.connectOrDisconnect(profile.index) },
        modifier = Modifier.testTag("profile.$digest.index.${profile.index}.role.action")
            .semantics { contentDescription = "Profile ${profile.index + 1} action" },
    )
}

@Composable
private fun OrphanRow(controller: SessionController, profile: ProfileData) {
    val status = controller.orphanStatus()
    ConnectionButton(
        name = profile.name,
        details = listOfNotNull(protocolName(profile.protocol), status?.label, "not in subscription"),
        status = status,
        action = controller.mainActionTitle,
        enabled = controller.mainActionEnabled,
        onClick = controller::mainAction,
        modifier = Modifier.testTag("profile.active"),
    )
}

@Composable
private fun ConnectionButton(
    name: String,
    details: List<String>,
    status: ProfileStatus?,
    action: String,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier,
) {
    val on = status == ProfileStatus.CONNECTED || status == ProfileStatus.RECONNECTING
    Row(
        Modifier.fillMaxWidth()
            .heightIn(min = 56.dp)
            .clip(ButtonShape)
            .background(Surface)
            .border(if (on) 3.dp else 1.5.dp, Ink, ButtonShape)
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick)
            .then(modifier)
            .padding(horizontal = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Column(Modifier.weight(1f)) {
            Text(name, color = Ink, fontSize = 17.sp, lineHeight = 20.sp, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                details.joinToString(" · "),
                color = if (on) Ink else Muted,
                fontSize = 13.sp,
                lineHeight = 16.sp,
                fontWeight = if (on) FontWeight.SemiBold else FontWeight.Normal,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(action, color = Ink, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
    }
}

private fun protocolName(protocol: String): String? = when (protocol) {
    "" -> null
    "OUTLINE" -> "Outline"
    "XRAY" -> "Xray"
    "TRUST_TUNNEL" -> "TrustTunnel"
    else -> protocol
}

@Composable
private fun Logs(controller: SessionController, modifier: Modifier) {
    val state = controller.state
    val density = LocalDensity.current
    var menuAt by remember { mutableStateOf<IntOffset?>(null) }
    Column(modifier.fillMaxWidth().background(Surface)) {
        Box(Modifier.fillMaxWidth().height(1.5.dp).background(Ink))
        Box(Modifier.fillMaxWidth().weight(1f)) {
            AndroidView(
                factory = { context ->
                    controller.createLogView(context).apply {
                        with(density) { setPadding(12.dp.roundToPx(), 6.dp.roundToPx(), 12.dp.roundToPx(), 0) }
                        onDoubleTap = { x, y -> menuAt = IntOffset(x, y) }
                    }
                },
                modifier = Modifier.fillMaxSize().clipToBounds(),
                update = {
                    it.update(state.logs, state.clearRevision)
                    controller.restoreRetainedLogView(it)
                },
            )
            menuAt?.let { position ->
                Box(Modifier.offset { position }) {
                    DropdownMenu(expanded = true, onDismissRequest = { menuAt = null }) {
                        DropdownMenuItem(
                            text = { Text("Send logs", Modifier.fillMaxWidth(), textAlign = TextAlign.Center) },
                            onClick = { menuAt = null; controller.exportLogs() },
                            enabled = !state.exportingLogs,
                            modifier = Modifier.testTag("logs.export"),
                        )
                        DropdownMenuItem(
                            text = { Text("Clear", Modifier.fillMaxWidth(), textAlign = TextAlign.Center) },
                            onClick = { menuAt = null; controller.clearLogs() },
                            modifier = Modifier.testTag("logs.clear"),
                        )
                    }
                }
            }
        }
        Spacer(Modifier.windowInsetsBottomHeight(WindowInsets.navigationBars))
    }
}

@Composable
private fun ErrorDialog(controller: SessionController) {
    val state = controller.state
    val loadFailed = state.loadError.isNotEmpty()
    val detail = listOf(state.loadError, state.error, state.session.failure, state.logsError).firstOrNull(String::isNotEmpty).orEmpty()
    var dismissed by rememberSaveable { mutableStateOf("") }
    // A new attempt clears the error, so the same failure shows its popup again.
    LaunchedEffect(detail) { if (detail.isEmpty()) dismissed = "" }
    if (detail.isEmpty() || detail == dismissed) return
    val close = { if (detail == state.error) controller.dismissError() else dismissed = detail }
    val status = controller.status
    Dialog(onDismissRequest = close) {
        Column(
            Modifier.fillMaxWidth()
                .semantics { testTagsAsResourceId = true }
                .testTag("error.dialog")
                .semantics { stateDescription = status }
                .clip(ButtonShape)
                .background(Surface)
                .border(1.5.dp, Ink, ButtonShape)
                .padding(start = 16.dp, end = 16.dp, top = 16.dp, bottom = 12.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                SockIcon(holey = true, width = 26.dp)
                Text(
                    if (loadFailed) "Couldn't load the subscription" else "Something went wrong",
                    color = Ink,
                    fontSize = 18.sp,
                    lineHeight = 22.sp,
                    fontWeight = FontWeight.SemiBold,
                )
            }
            if (loadFailed) {
                Text(
                    "Check the link and whether another VPN is blocking it.",
                    color = Muted,
                    fontSize = 14.sp,
                    lineHeight = 20.sp,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
            Row(
                Modifier.padding(top = 10.dp)
                    .fillMaxWidth()
                    .border(1.dp, Rule, RoundedCornerShape(6.dp))
                    .padding(horizontal = 10.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    detail,
                    color = Ink,
                    fontSize = 11.5.sp,
                    lineHeight = 16.sp,
                    fontFamily = FontFamily.Monospace,
                    maxLines = 4,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    "Copy",
                    color = Ink,
                    fontSize = 12.sp,
                    fontWeight = FontWeight.SemiBold,
                    textDecoration = TextDecoration.Underline,
                    modifier = Modifier.clickable(role = Role.Button) { controller.copyToClipboard(detail) },
                )
            }
            Row(Modifier.padding(top = 14.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                DialogButton("Close", filled = false, modifier = Modifier.weight(1f), onClick = close)
                if (loadFailed) {
                    DialogButton(
                        "Retry",
                        filled = true,
                        modifier = Modifier.weight(1f).testTag("subscription.retry"),
                    ) { dismissed = ""; controller.retryLoad() }
                }
            }
        }
    }
}

@Composable
private fun DialogButton(title: String, filled: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val shape = RoundedCornerShape(8.dp)
    Box(
        modifier.height(44.dp)
            .clip(shape)
            .then(if (filled) Modifier.background(Ink) else Modifier.border(1.5.dp, Ink, shape))
            .clickable(role = Role.Button, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Text(title, color = if (filled) Surface else Ink, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
    }
}

private val SockPath = PathParser().parsePathString(
    "M17 3 H29 Q30.5 3 30.5 4.5 V27 Q32 36 26 40 L14 46.5 Q8 49 4.8 44.5 Q2.2 39.5 7.5 36 L13 32.5 Q15.5 30.8 15.5 27.5 V4.5 Q15.5 3 17 3 Z",
).toPath()
private val HoleRays = PathParser().parsePathString(
    "M5.6 39.6 l-1.5 -0.7 M6.4 43.8 l-1.2 1.1 M9.8 45.5 l-0.3 1.6 M13.6 44.6 l1 1.3 M15.6 41.2 l1.6 0.2 M14 37.6 l1.1 -1.3 M10.2 36.6 l-0.2 -1.6 M7 37.2 l-1 -1.2",
).toPath()

@Composable
private fun SockIcon(holey: Boolean, width: Dp, modifier: Modifier = Modifier) {
    Canvas(modifier.size(width, width * 1.25f)) {
        scale(size.width / 40f, pivot = Offset.Zero) {
            if (holey) {
                drawPath(SockPath, Paper)
                drawPath(SockPath, Ink, style = Stroke(2.5f, cap = StrokeCap.Round, pathEffect = PathEffect.dashPathEffect(floatArrayOf(3.2f, 2.8f))))
                rotate(-25f, pivot = Offset(10.5f, 41f)) {
                    drawOval(Ink, topLeft = Offset(6.9f, 38.1f), size = Size(7.2f, 5.8f))
                }
                drawPath(HoleRays, Ink, style = Stroke(1.2f, cap = StrokeCap.Round))
            } else {
                drawPath(SockPath, Surface)
                drawPath(SockPath, Ink, style = Stroke(2.5f))
                drawLine(Ink, Offset(15.5f, 8.5f), Offset(30.5f, 8.5f), 2f)
                drawLine(Ink, Offset(15.5f, 12.5f), Offset(30.5f, 12.5f), 2f)
            }
        }
    }
}
