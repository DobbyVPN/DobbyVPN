package com.dobby.feature.logging.ui

import kotlinx.coroutines.flow.StateFlow

data class LogsUiState(
    val logMessages: StateFlow<List<String>>
)
