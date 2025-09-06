package com.dobby.feature.main.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.dobby.feature.main.presentation.AwgConnectionState
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.util.koinViewModel

@Composable
fun AwgScreen(
    viewModel: MainViewModel = koinViewModel(),
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.Center
    ) {
        Text(
            text = "AmneziaWG ${viewModel.awgVersion}",
            fontSize = 24.sp,
            color = Color.Black,
            modifier = Modifier.padding(horizontal = 8.dp)
        )

        Spacer(modifier = Modifier.height(16.dp))

        TextField(
            value = viewModel.awgConfigState.value,
            onValueChange = { viewModel.onAwgConfigEdit(it) },
            minLines = 8,
            maxLines = 8,
            modifier = Modifier.fillMaxWidth().wrapContentHeight(),
            label = { Text("Config") },
        )

        Spacer(modifier = Modifier.height(16.dp))

        Button(
            onClick = {
                when (viewModel.awgConnectionState.value) {
                    AwgConnectionState.ON -> viewModel.onAwgDisconnect()
                    AwgConnectionState.OFF -> viewModel.onAwgConnect()
                }
            },
            shape = RoundedCornerShape(6.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = Color.Black,
                contentColor = Color.White
            ),
            modifier = Modifier.fillMaxWidth()
        ) {

            when (viewModel.awgConnectionState.value) {
                AwgConnectionState.ON -> Text("Disconnect")
                AwgConnectionState.OFF -> Text("Connect")
            }
        }
    }
}