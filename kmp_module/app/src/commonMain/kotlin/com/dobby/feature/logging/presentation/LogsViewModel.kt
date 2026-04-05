package com.dobby.feature.logging.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.ui.LogsUiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

class LogsViewModel(
    private val logsRepository: LogsRepository,
    private val logEventsChannel: LogEventsChannel,
    private val copyLogsInteractor: CopyLogsInteractor
) : ViewModel() {

    private val _uiState = MutableStateFlow(LogsUiState())
    val uiState: StateFlow<LogsUiState> = _uiState.asStateFlow()

    init {
        viewModelScope.launch {
            logEventsChannel.logEvents.collect { line ->
                _uiState.update { state ->
                    state.copy(
                        logMessages = (state.logMessages + line)
                            .takeLast(LogsRepository.UI_TAIL_LINES)
                    )
                }
            }
        }
    }

    fun clearLogs() {
        logsRepository.clearLogs()
        logEventsChannel.clear()
        _uiState.value = LogsUiState(emptyList())
    }

    fun copyLogsToClipBoard() {
        copyLogsInteractor.copy(logsRepository.readAllLogs())
    }
}
