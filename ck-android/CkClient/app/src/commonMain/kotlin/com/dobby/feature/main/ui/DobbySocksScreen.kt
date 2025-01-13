package com.dobby.feature.main.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.navigation.AmneziaWGScreen
import com.dobby.navigation.LogsScreen
import org.jetbrains.compose.ui.tooling.preview.Preview


@Preview
@Composable
fun DobbySocksScreen(
    modifier: Modifier = Modifier,
    navController: NavController,
    viewModel: MainViewModel = viewModel(),
) {
    val scrollState = rememberScrollState()

    val isCloakEnabled = remember { mutableStateOf(true) }
    val focusRequester1 = remember { FocusRequester() }
    val uiState by viewModel.uiState.collectAsState()

    var cloakJson by remember { mutableStateOf(uiState.cloakJson) }
    var apiKey by remember { mutableStateOf(uiState.outlineKey) }

    Column(
        modifier = modifier
            .fillMaxSize()
            .background(Color.White)
            .verticalScroll(scrollState)
            .padding(16.dp),
        verticalArrangement = Arrangement.Center
    ) {
        Button(
            onClick = { navController.navigate(AmneziaWGScreen) }
        ) {
            Text("Go to AmneziaWG VPN")
        }

        Spacer(modifier = Modifier.height(16.dp))

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
                tagText = if (uiState.isConnected) "connected" else "disconnected",
                color = if (uiState.isConnected) 0xFFDCFCE7 else 0xFFFEE2E2
            )
        }

        Spacer(modifier = Modifier.height(16.dp))

        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = 12.dp),
            horizontalArrangement = Arrangement.Start,
            verticalAlignment = Alignment.CenterVertically
        ) {

            Switch(
                modifier = Modifier
                    .size(44.dp, 24.dp),
                checked = isCloakEnabled.value,
                onCheckedChange = { isCloakEnabled.value = it },
                colors = SwitchDefaults.colors(
                    checkedThumbColor = Color.White,
                    uncheckedThumbColor = Color.White,
                    checkedTrackColor = Color.Black,
                    uncheckedTrackColor = Color(0xFFE2E8F0)
                )
            )
            Text(
                text = "Enable cloak?",
                color = Color.Black,
                modifier = Modifier.padding(horizontal = 8.dp)
            )
        }
        TextField(
            value = cloakJson,
            onValueChange = { cloakJson = it },
            label = { Text("Enter Cloak JSON") },
            singleLine = false,
            colors = TextFieldDefaults.colors(
                unfocusedPlaceholderColor = Color(0xFF94A3B8),

                ),
            enabled = isCloakEnabled.value,
            keyboardActions = KeyboardActions(
                onDone = { focusRequester1.requestFocus() }
            ),
            modifier = Modifier
                .clip(RoundedCornerShape(6.dp))
                .fillMaxWidth()
        )

        Spacer(modifier = Modifier.height(16.dp))

        TextField(
            value = apiKey,
            onValueChange = { apiKey = it },
            label = { Text("Enter outline config") },
            singleLine = false,
            modifier = Modifier.fillMaxWidth()
                .clip(RoundedCornerShape(6.dp))
                .focusRequester(focusRequester1)
        )
        Spacer(modifier = Modifier.height(16.dp))

        Button(
            onClick = {
                viewModel.onConnectionButtonClicked(cloakJson, apiKey, isCloakEnabled.value)
            },
            shape = RoundedCornerShape(6.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = Color.Black,
                contentColor = Color.White
            ),
            modifier = Modifier.fillMaxWidth()
        ) {
            Text(if (uiState.isConnected) "Disconnect" else "Connect")
        }

        Spacer(modifier = Modifier.height(16.dp))

        Button(
            onClick = { navController.navigate(LogsScreen) },
            shape = RoundedCornerShape(6.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = Color.White,
                contentColor = Color.Black
            ),
            modifier = Modifier.fillMaxWidth()
                .border(1.dp, Color.Black, shape = RoundedCornerShape(6.dp)),
        ) {
            Text("Show Logs")
        }
    }
}
