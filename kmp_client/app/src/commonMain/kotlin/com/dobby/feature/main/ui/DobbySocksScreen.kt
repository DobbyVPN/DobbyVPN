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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.util.koinViewModel
import org.jetbrains.compose.ui.tooling.preview.Preview

@Preview
@Composable
fun DobbySocksScreen(
    mainViewModel: MainViewModel = koinViewModel(),
    logsViewModel: LogsViewModel = koinViewModel(),
    modifier: Modifier = Modifier,
) {
    val uiMainState by mainViewModel.uiState.collectAsState()
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
                    tagText = if (uiMainState.isConnected) "Status: connected" else "Status: disconnected",
                    color = if (uiMainState.isConnected) 0xFFDCFCE7 else 0xFFFEE2E2
                )
                Spacer(modifier = Modifier.weight(1f))
            }

            Spacer(modifier = Modifier.height(16.dp))

            TextField(
                value = uiMainState.connectionURL,
                onValueChange = mainViewModel::onConnectionUrlChanged,
                label = { Text("Subscription URL") },
                singleLine = false,
                minLines = 3,
                maxLines = 3,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(6.dp))
            )

            Spacer(modifier = Modifier.height(16.dp))

            Button(
                onClick = {
                    mainViewModel.onConnectionButtonClicked(uiMainState.connectionURL)
                },
                shape = RoundedCornerShape(6.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color.Black,
                    contentColor = Color.White
                ),
                modifier = Modifier.fillMaxWidth()
            ) {
                Text(if (uiMainState.isVpnStarted) "Stop" else "Start")
            }
        }

        val listState = rememberLazyListState()

        LaunchedEffect(uiLogState.logMessages.size) {
            if (uiLogState.logMessages.isNotEmpty()) {
                listState.animateScrollToItem(uiLogState.logMessages.lastIndex)
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
