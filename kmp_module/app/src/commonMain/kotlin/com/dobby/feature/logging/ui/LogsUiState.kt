package com.dobby.feature.logging.ui

import com.dobby.feature.logging.domain.LogStorageStatus

data class LogsUiState(
    val logMessages: List<String> = emptyList(),
    val storageStatus: LogStorageStatus = LogStorageStatus.READY,
)
