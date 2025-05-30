package com.dobby.feature.logging.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.ui.LogsUiState
import kotlinx.coroutines.flow.StateFlow

class LogsViewModel(
    private val logsRepository: LogsRepository,
    private val copyLogsInteractor: CopyLogsInteractor
): ViewModel() {

    val uiState: LogsUiState = LogsUiState(logsRepository.readLogs())

    fun clearLogs() {
        logsRepository.clearLogs()
    }

    fun reloadLogs() {
    }

    fun copyLogsToClipBoard() {
        copyLogsInteractor.copy(uiState.logMessages.toList())
    }
}
