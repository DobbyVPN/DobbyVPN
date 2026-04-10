package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.wrapContentWidth
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch


@Composable
fun DnsDiagnosticCard(
    hostname: String,
    modifier: Modifier = Modifier,
) {
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
        Column {
            DnsDiagnosticHeader("DNS diagnostic")
            DnsDiagnosticContent(hostname)
        }
    }
}

@Composable
private fun DnsDiagnosticContent(
    hostname: String,
    viewModel: DiagnosticViewModel = viewModel(),
) {
    val coroutineScope = rememberCoroutineScope()

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(8.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column {
            Row {
                Text("Hostname:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(hostname)
            }
            Row {
                Text("IP:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(viewModel.uiState.value.dnsData.ip)
            }
            Row {
                Text("City:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(viewModel.uiState.value.dnsData.city)
            }
            Row {
                Text("Country:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(viewModel.uiState.value.dnsData.country)
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
                            viewModel.reloadDnsIpData(hostname)
                        }
                    }
                )
        )
    }
}
