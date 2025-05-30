package com.dobby.feature.logging.ui

import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.snapshots.SnapshotStateList

data class LogsUiState(
    val logMessages: SnapshotStateList<String> = mutableStateListOf()
)
