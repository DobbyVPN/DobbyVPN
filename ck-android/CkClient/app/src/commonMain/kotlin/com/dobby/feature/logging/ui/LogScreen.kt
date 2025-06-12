package com.dobby.feature.logging.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.BuildConfig

@Composable
fun LogScreen(
    viewModel: LogsViewModel = viewModel(),
    modifier: Modifier = Modifier,
) {

    val logs by viewModel.uiState.logMessages.collectAsState()

    Column(modifier = modifier) {
        Text(
            BuildConfig.VERSION_NAME
        )

        Button(
            onClick = { viewModel.copyLogsToClipBoard() },
            shape = RoundedCornerShape(6.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = Color.Black,
            ),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 2.dp)
        ) {
            Text("Copy logs to clipboard")
        }

        Button(
            onClick = { viewModel.clearLogs() },
            shape = RoundedCornerShape(6.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = Color.White,
                contentColor = Color.Black
            ),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 2.dp)
        ) {
            Text("Clear Logs")
        }

        LazyColumn {
            items(logs) { message ->
                // some important logs contain this
                val isBald = message.contains("!!!")

                Text(
                    text = message,
                    modifier = Modifier.padding(8.dp),
                    fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                    fontWeight = if (isBald) FontWeight.W700 else FontWeight.W400,
                )

                HorizontalDivider(thickness = 1.dp, color = Color.Gray)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun Toolbar(
    modifier: Modifier = Modifier,
    onBackClicked: () -> Unit
) {
    TopAppBar(
        title = {
            Text(
                text = "Back",
                style = TextStyle(
                    fontSize = 20.sp,
                    fontWeight = FontWeight.W600,
                    color = Color.Black
                )
            )
        },
        navigationIcon = {
            IconButton(onClick = onBackClicked) {
                Icon(
                    imageVector = Icons.Filled.ArrowBack,
                    contentDescription = "Back",
                    tint = Color.Black
                )
            }
        },
        modifier = modifier,
        colors = TopAppBarDefaults.mediumTopAppBarColors(
            containerColor = Color.White
        )
    )
}
