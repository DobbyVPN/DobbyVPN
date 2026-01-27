package com.dobby.feature.diagnostic.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Modifier
import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel
import com.dobby.util.koinViewModel

private const val DEFAULT_DNS_RESOLVING_HOST = "www.google.com"

@Composable
fun DiagnosticScreen(
    viewModel: DiagnosticViewModel = koinViewModel(),
    modifier: Modifier = Modifier,
) {
    LaunchedEffect(Unit) {
        viewModel.reloadIpData()
        viewModel.reloadDnsIpData(hostname = DEFAULT_DNS_RESOLVING_HOST)
    }

    Column(modifier = modifier) {
        IpDiagnosticCard()
        DnsDiagnosticCard(DEFAULT_DNS_RESOLVING_HOST)
    }
}
