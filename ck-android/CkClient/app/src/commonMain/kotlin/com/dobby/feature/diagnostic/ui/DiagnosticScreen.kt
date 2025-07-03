package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel
import com.dobby.util.koinViewModel
import kotlinx.coroutines.launch

private const val DEFAULT_DNS_RESOLVING_HOST = "www.google.com"

@Composable
fun DiagnosticScreen(
    viewModel: DiagnosticViewModel = koinViewModel(),
    modifier: Modifier = Modifier,
) {
    val coroutineScope = rememberCoroutineScope()

    LaunchedEffect(Unit) {
        coroutineScope.launch {
            viewModel.reloadIpData()
            viewModel.reloadDnsIpData(hostname = DEFAULT_DNS_RESOLVING_HOST)
        }
    }

    Column(modifier = modifier) {
        IpDiagnosticCard()
        DnsDiagnosticCard(DEFAULT_DNS_RESOLVING_HOST)
    }
}
