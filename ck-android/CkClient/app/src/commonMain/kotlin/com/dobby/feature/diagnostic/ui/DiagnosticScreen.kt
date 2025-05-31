package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.wrapContentWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel
import kotlinx.coroutines.launch

@Composable
fun DiagnosticScreen(
    viewModel: DiagnosticViewModel = viewModel(),
    modifier: Modifier = Modifier,
) {
    val coroutineScope = rememberCoroutineScope()

    remember {
        coroutineScope.launch {
            viewModel.reloadIpData()
        }
    }

    Column(modifier = modifier) {
        IpDiagnosticCard()
    }
}

@Composable
private fun IpDiagnosticCard(
    viewModel: DiagnosticViewModel = viewModel(),
    modifier: Modifier = Modifier,
) {
    val coroutineScope = rememberCoroutineScope()

    Card(
        modifier = modifier
            .fillMaxWidth()
            .padding(16.dp),
        shape = CardDefaults.elevatedShape,
        colors = CardDefaults.outlinedCardColors(),
        elevation = CardDefaults.cardElevation(),
        border = BorderStroke(
            width = 2.dp,
            color = MaterialTheme.colorScheme.outline
        ),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(8.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Column(
                modifier = Modifier,
                horizontalAlignment = Alignment.Start,
                verticalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                Row {
                    Text(
                        text = "IP:",
                        modifier = Modifier
                            .wrapContentWidth()
                            .padding(end = 10.dp),
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        fontWeight = FontWeight.Bold,
                        minLines = 1,
                        maxLines = 1,
                    )
                    Text(
                        text = viewModel.uiState.value.ip,
                        modifier = Modifier,
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        minLines = 1,
                        maxLines = 1,
                    )
                }

                Row {
                    Text(
                        text = "City:",
                        modifier = Modifier
                            .wrapContentWidth()
                            .padding(end = 10.dp),
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        fontWeight = FontWeight.Bold,
                        minLines = 1,
                        maxLines = 1,
                    )
                    Text(
                        text = viewModel.uiState.value.city,
                        modifier = Modifier,
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        minLines = 1,
                        maxLines = 1,
                    )
                }

                Row {
                    Text(
                        text = "Country:",
                        modifier = Modifier
                            .wrapContentWidth()
                            .padding(end = 10.dp),
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        fontWeight = FontWeight.Bold,
                        minLines = 1,
                        maxLines = 1,
                    )
                    Text(
                        text = viewModel.uiState.value.country,
                        modifier = Modifier,
                        fontSize = MaterialTheme.typography.bodyLarge.fontSize,
                        minLines = 1,
                        maxLines = 1,
                    )
                }
            }

            Icon(
                imageVector = Icons.Default.Refresh,
                contentDescription = "Refresh button",
                modifier = Modifier
                    .size(48.dp)
                    .clickable(
                        enabled = true,
                        onClickLabel = null,
                        role = Role.Image,
                        onClick = {
                            coroutineScope.launch {
                                viewModel.reloadIpData()
                            }
                        }
                    )
            )
        }
    }
}