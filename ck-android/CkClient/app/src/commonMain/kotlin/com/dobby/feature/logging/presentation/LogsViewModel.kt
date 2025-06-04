package com.dobby.feature.logging.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.ui.LogsUiState

class LogsViewModel(
    private val logsRepository: LogsRepository,
    private val copyLogsInteractor: CopyLogsInteractor
): ViewModel() {

    val uiState: LogsUiState = LogsUiState(logsRepository.logState)

    fun clearLogs() {
        logsRepository.clearLogs()
    }

    fun copyLogsToClipBoard() {
        copyLogsInteractor.copy(uiState.logMessages.value)
    }
}
