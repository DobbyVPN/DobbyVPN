package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp

@Composable
fun DnsDiagnosticHeader(
    title: String,
    modifier: Modifier = Modifier,
) {
    Column {
        Text(
            text = title,
            modifier = modifier.fillMaxWidth().padding(8.dp),
            fontSize = MaterialTheme.typography.headlineMedium.fontSize,
            fontWeight = MaterialTheme.typography.headlineMedium.fontWeight,
            maxLines = 1,
            minLines = 1,
            textAlign = TextAlign.Center
        )

        HorizontalDivider(
            modifier = Modifier
                .fillMaxWidth(),
            thickness = 2.dp,
            color = MaterialTheme.colorScheme.outline,
        )
    }
}
