package com.dobby.feature.main.ui

import androidx.compose.foundation.background
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.util.koinViewModel
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
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
    val isCloakEnabled = remember { mutableStateOf(uiMainState.isCloakEnabled) }

    var cloakJson by remember { mutableStateOf(uiMainState.cloakJson) }
    var apiKey by remember { mutableStateOf(uiMainState.outlineKey) }

    MainScope().launch {
        while (true) {
            logsViewModel.reloadLogs()
            delay(1000L)
        }
    }

    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.SpaceBetween
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f),
            verticalArrangement = Arrangement.Center
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(8.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = "Status",
                    fontSize = 24.sp,
                    color = Color.Black,
                    modifier = Modifier.padding(end = 8.dp)
                )

                Spacer(modifier = Modifier.weight(1f))

                TagChip(
                    tagText = if (uiMainState.isConnected) "connected" else "disconnected",
                    color = if (uiMainState.isConnected) 0xFFDCFCE7 else 0xFFFEE2E2
                )
            }

            Spacer(modifier = Modifier.height(16.dp))

            TextField(
                value = apiKey,
                onValueChange = { apiKey = it },
                label = { Text("Enter config") },
                singleLine = false,
                modifier = Modifier
                    .fillMaxWidth()
                    .fillMaxHeight(0.3f)
                    .clip(RoundedCornerShape(6.dp))
            )

            Spacer(modifier = Modifier.height(16.dp))

            Button(
                onClick = {
                    mainViewModel.onConnectionButtonClicked(cloakJson, apiKey, isCloakEnabled.value)
                },
                shape = RoundedCornerShape(6.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color.Black,
                    contentColor = Color.White
                ),
                modifier = Modifier.fillMaxWidth()
            ) {
                Text(if (uiMainState.isConnected) "Disconnect" else "Connect")
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
                .fillMaxHeight(0.25f)
                .clip(RoundedCornerShape(6.dp))
                .background(Color.Gray.copy(alpha = 0.1f))
                .padding(8.dp)
        ) {
            LazyColumn(state = listState) {
                items(uiLogState.logMessages) { message ->
                    val isBold = message.contains("!!!")

                    Text(
                        text = message,
                        modifier = Modifier.padding(8.dp),
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = if (isBold) FontWeight.W700 else FontWeight.W400,
                        color = Color.Black
                    )

                    HorizontalDivider(thickness = 1.dp, color = Color.DarkGray)
                }
            }
        }
    }
}
