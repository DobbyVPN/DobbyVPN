package com.dobby.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.dobby.feature.main.presentation.MainViewModel

const val INITIAL_CONTENT = """[Interface]
PrivateKey = <...>
Address = <...>
DNS = 8.8.8.8
Jc = 0
Jmin = 0
Jmax = 0
S1 = 0
S2 = 0
H1 = 1
H2 = 2
H3 = 3
H4 = 4

[Peer]
PublicKey = <...>
Endpoint = <...>
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 60"""

@Composable
fun AmneziaWGApp(
    modifier: Modifier,
    navController: NavController,
    viewModel: MainViewModel = viewModel(),
) {
    var content by remember { mutableStateOf(INITIAL_CONTENT) }
    var status by remember { mutableStateOf("") }

    Column(
        modifier = modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center
    ) {
        Text("AmneziaWG ${viewModel.getAwgVersion()}")

        Spacer(modifier = Modifier.height(16.dp))

        TextField(
            value = content,
            onValueChange = { content = it },
            minLines = 8,
            maxLines = 8,
            modifier = Modifier.fillMaxWidth().wrapContentHeight().padding(16.dp),
            label = { Text("Config") },
        )

        Spacer(modifier = Modifier.height(16.dp))

        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 0.dp),
            horizontalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            Button(
                onClick = {
                    viewModel.onAwgConnect(content)
                    status = "Connected"
                },
                modifier = Modifier.weight(1.0f)
            ) {
                Text("Connect")
            }
            Button(
                onClick = {
                    viewModel.onAwgDisconnect()
                    status = "Disconnected"
                },
                modifier = Modifier.weight(1.0f)
            ) {
                Text("Disconnect")
            }
        }

        Spacer(modifier = Modifier.height(16.dp))

        Text(status)
    }
}