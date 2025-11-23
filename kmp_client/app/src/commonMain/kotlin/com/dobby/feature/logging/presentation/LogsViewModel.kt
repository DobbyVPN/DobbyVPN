// LogsViewModel.kt
package com.dobby.feature.logging.presentation

import androidx.compose.ui.util.fastJoinToString
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.ui.LogsUiState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class LogsViewModel(
    private val logsRepository: LogsRepository,
    private val copyLogsInteractor: CopyLogsInteractor
) : ViewModel() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    private val _uiState = MutableStateFlow(LogsUiState())
    val uiState: StateFlow<LogsUiState> = _uiState.asStateFlow()

    init {
        scope.launch {
            logsRepository.logState.collect { newLogList ->
                _uiState.value = LogsUiState(newLogList.toList())
            }
        }
    }

    fun clearLogs() {
        logsRepository.clearLogs()
    }

    fun copyLogsToClipBoard() {
        copyLogsInteractor.copy(logsRepository.readAllLogs())
    }

    fun reloadLogs() {
        scope.launch {
            val freshLogs = logsRepository.readAllLogs().takeLast(50)
            _uiState.value = _uiState.value.copy(logMessages = freshLogs.toList())
        }
    }

    override fun onCleared() {
        super.onCleared()
        scope.cancel()
    }

    fun dispose() {
        scope.cancel()
    }
}
