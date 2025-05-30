package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel

@Composable
fun DiagnosticScreen(
    viewModel: DiagnosticViewModel = viewModel(),
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier) {
        Surface(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            shape = RoundedCornerShape(10.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.secondary),
            shadowElevation = 5.dp,
        ) {
            // Ip diagnostic
            Row(
                modifier = Modifier.padding(8.dp),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(
                    modifier = Modifier,
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(10.dp)
                ) {
                    Text(
                        text = viewModel.uiState.value.ip,
                        modifier = Modifier,
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        fontStyle = MaterialTheme.typography.bodyLarge.fontStyle,
                        fontWeight = MaterialTheme.typography.bodyLarge.fontWeight,
                        minLines = 1,
                        maxLines = 1,
                    )
                }

                Icon(
                    imageVector = Icons.Default.Refresh,
                    contentDescription = "Refresh button",
                    modifier = Modifier
                        .size(32.dp)
                        .clickable(
                            enabled = true,
                            onClickLabel = null,
                            role = Role.Image,
                            onClick = viewModel::reloadIpData,
                        )
                )
            }
        }
    }
}