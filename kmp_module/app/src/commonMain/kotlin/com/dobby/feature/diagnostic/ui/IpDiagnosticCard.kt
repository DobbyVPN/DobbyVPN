package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
fun IpDiagnosticCard(
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
        Column {
            DnsDiagnosticHeader("IP diagnostic")
            IpDiagnosticContent(viewModel, coroutineScope)
        }
    }
}

@Composable
private fun IpDiagnosticContent(
    viewModel: DiagnosticViewModel,
    coroutineScope: CoroutineScope
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(8.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column {
            Row {
                Text("IP:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(viewModel.uiState.value.ipData.ip)
            }
            Row {
                Text("City:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(viewModel.uiState.value.ipData.city)
            }
            Row {
                Text("Country:", fontWeight = FontWeight.Bold)
                VerticalDivider(Modifier.height(0.dp), 10.dp)
                Text(viewModel.uiState.value.ipData.country)
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
