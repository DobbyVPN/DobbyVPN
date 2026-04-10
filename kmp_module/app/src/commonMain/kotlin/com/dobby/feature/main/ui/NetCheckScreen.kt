package com.dobby.feature.main.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.netcheck.presentation.NetCheckViewModel
import com.dobby.feature.netcheck.ui.NetCheckStatus
import org.jetbrains.compose.ui.tooling.preview.Preview
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.TimeMark
import kotlin.time.TimeSource

@Preview
@Composable
fun NetCheckScreen(
    logsViewModel: LogsViewModel,
    netCheckViewModel: NetCheckViewModel,
    modifier: Modifier = Modifier,
) {
    val uiNetCheckState by netCheckViewModel.uiState.collectAsState()
    val uiLogState by logsViewModel.uiState.collectAsState()

    var showLogsDialog by remember { mutableStateOf(false) }

    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.Top
    ) {
        Column(
            modifier = Modifier.fillMaxWidth(),
            verticalArrangement = Arrangement.Top
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(8.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Spacer(modifier = Modifier.weight(1f))
                TagChip(
                    tagText = when (uiNetCheckState.netCheckStatus) {
                        NetCheckStatus.ON -> "Status: connected"
                        NetCheckStatus.OFF -> "Status: disconnected"
                        NetCheckStatus.FAILED -> "Status: error ${uiNetCheckState.description}"
                    },
                    color = when (uiNetCheckState.netCheckStatus) {
                        NetCheckStatus.ON -> 0xFFDCFCE7
                        NetCheckStatus.OFF -> 0xFFFEE2E2
                        NetCheckStatus.FAILED -> 0xFF4444E2
                    },
                )
                Spacer(modifier = Modifier.weight(1f))
            }

            Spacer(modifier = Modifier.height(16.dp))

            TextField(
                value = uiNetCheckState.netCheckConfig,
                onValueChange = netCheckViewModel::updateConfig,
                label = { Text("Net Check") },
                singleLine = false,
                minLines = 9,
                maxLines = 9,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(6.dp))
            )

            Spacer(modifier = Modifier.height(16.dp))

            Button(
                onClick = netCheckViewModel::onButtonClicked,
                shape = RoundedCornerShape(6.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color.Black,
                    contentColor = Color.White
                ),
                modifier = Modifier.fillMaxWidth()
            ) {
                Text(if (uiNetCheckState.netCheckStatus == NetCheckStatus.ON) "Stop" else "Start")
            }
        }

        val listState = rememberLazyListState()
        val lastAutoScrollMark = remember { mutableStateOf<TimeMark?>(null) }

        LaunchedEffect(uiLogState.logMessages.size) {
            if (uiLogState.logMessages.isNotEmpty()) {
                val lastIndex = uiLogState.logMessages.lastIndex
                val visible = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
                val nearBottom = visible >= (lastIndex - 1)
                val allowScroll = lastAutoScrollMark.value?.elapsedNow()?.let { it >= 500.milliseconds } ?: true
                if (nearBottom && allowScroll) {
                    lastAutoScrollMark.value = TimeSource.Monotonic.markNow()
                    listState.animateScrollToItem(lastIndex)
                }
            }
        }

        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .clip(RoundedCornerShape(6.dp))
                .background(Color.Gray.copy(alpha = 0.1f))
                .padding(8.dp)
                .pointerInput(Unit) {
                    detectTapGestures(
                        onDoubleTap = {
                            showLogsDialog = true
                        }
                    )
                }
        ) {
            LazyColumn(state = listState) {
                items(uiLogState.logMessages) { message ->
                    Text(
                        buildAnnotatedString {
                            withStyle(
                                style = SpanStyle(
                                    fontWeight = FontWeight.W700,
                                )
                            ) {
                                append("> ")
                            }

                            withStyle(
                                style = SpanStyle(
                                    fontWeight = FontWeight.W400,
                                )
                            ) {
                                append(message)
                            }
                        },
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 0.dp, horizontal = 4.dp),
                        fontSize = 14.sp,
                        fontFamily = FontFamily.Monospace,
                        color = Color.Black
                    )
                }
            }
        }
    }

    if (showLogsDialog) {
        AlertDialog(
            onDismissRequest = { showLogsDialog = false },
            title = {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        text = "Logs",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold
                    )
                    IconButton(onClick = { showLogsDialog = false }) {
                        Text("✕", fontSize = 18.sp)
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    logsViewModel.copyLogsToClipBoard()
                    showLogsDialog = false
                }) {
                    Text("Send")
                }
            },
            dismissButton = {
                TextButton(onClick = {
                    logsViewModel.clearLogs()
                    showLogsDialog = false
                }) {
                    Text("Clear")
                }
            }
        )
    }
}
