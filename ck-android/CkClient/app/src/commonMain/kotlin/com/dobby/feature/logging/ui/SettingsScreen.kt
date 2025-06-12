package com.dobby.feature.logging.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.dobby.BuildConfig
import com.dobby.navigation.DiagnosticsScreen
import com.dobby.navigation.LogsScreen

@Composable
fun SettingsScreen(
    modifier: Modifier = Modifier,
    onNavigate: (Any) -> Unit = {}
) {
    Column(modifier = modifier) {
        Box(
            modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 0.dp, bottom = 16.dp)
        ) {
            Text(
                text = "DobbyVPN v${BuildConfig.VERSION_NAME}",
                fontSize = MaterialTheme.typography.headlineMedium.fontSize,
                maxLines = 1
            )
        }

        Column(
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Box(
                modifier = Modifier
                    .padding(horizontal = 8.dp)
                    .fillMaxWidth()
                    .border(
                        border = BorderStroke(
                            width = 2.dp,
                            color = MaterialTheme.colorScheme.outline
                        ),
                        shape = RectangleShape
                    )
                    .clickable {
                        onNavigate.invoke(LogsScreen)
                    }
            ) {
                Column(modifier = Modifier.padding(8.dp)) {
                    Text(
                        text = "Application log",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Bold,
                    )
                    Text(
                        text = "Logs may assist with debugging",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                    )
                }
            }

            Box(
                modifier = Modifier
                    .padding(horizontal = 8.dp)
                    .fillMaxWidth()
                    .border(
                        border = BorderStroke(
                            width = 2.dp,
                            color = MaterialTheme.colorScheme.outline
                        ),
                        shape = RectangleShape
                    )
                    .clickable {
                        onNavigate.invoke(DiagnosticsScreen)
                    }
            ) {
                Column(modifier = Modifier.padding(8.dp)) {
                    Text(
                        text = "Diagnostics page",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Bold,
                    )
                    Text(
                        text = "Diagnostics may assist with internet connection check",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                    )
                }
            }
        }
    }
}
