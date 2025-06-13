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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.dobby.navigation.AboutScreen
import com.dobby.navigation.DiagnosticsScreen
import com.dobby.navigation.LogsScreen

@Composable
fun SettingsScreen(
    modifier: Modifier = Modifier,
    onNavigate: (Any) -> Unit = {}
) {
    Column(modifier = modifier) {
        Text(
            text = "DobbyVPN",
            fontSize = MaterialTheme.typography.headlineMedium.fontSize,
            maxLines = 1,
            modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 0.dp, bottom = 16.dp)
        )

        Column(
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            SettingsBox(
                "Application log",
                "Logs may assist with debugging",
                modifier = Modifier
                    .padding(horizontal = 8.dp)
                    .clickable { onNavigate.invoke(LogsScreen) },
            )

            SettingsBox(
                "Diagnostics page",
                "Diagnostics may assist with internet connection check",
                modifier = Modifier
                    .padding(horizontal = 8.dp)
                    .clickable { onNavigate.invoke(DiagnosticsScreen) },
            )

            SettingsBox(
                "About",
                "Project short information",
                modifier = Modifier
                    .padding(horizontal = 8.dp)
                    .clickable { onNavigate.invoke(AboutScreen) },
            )
        }
    }
}

@Composable
fun SettingsBox(
    title: String,
    description: String,
    modifier: Modifier = Modifier
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .border(BorderStroke(2.dp, MaterialTheme.colorScheme.outline))
    ) {
        Column(modifier = Modifier.padding(8.dp)) {
            Text(
                text = title,
                fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                fontWeight = FontWeight.Bold,
            )
            Text(
                text = description,
                fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                fontWeight = FontWeight.Normal,
            )
        }
    }
}
