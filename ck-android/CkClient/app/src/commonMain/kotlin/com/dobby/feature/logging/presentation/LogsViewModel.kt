package com.dobby.feature.logging.presentation

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

    // универсальный scope, работает и на iOS, и на Android
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    private val _uiState = MutableStateFlow(LogsUiState())
    val uiState: StateFlow<LogsUiState> = _uiState.asStateFlow()

    init {
        // подписываемся на изменения логов
        scope.launch {
            logsRepository.logState.collect { newLogList ->
                _uiState.value = LogsUiState(newLogList)
            }
        }
    }

    fun clearLogs() {
        logsRepository.clearLogs()
    }

    fun copyLogsToClipBoard() {
        copyLogsInteractor.copy(uiState.value.logMessages)
    }

    override fun onCleared() {
        super.onCleared()
        scope.cancel() // Android lifecycle
    }

    // на iOS нужно будет вызывать вручную
    fun dispose() {
        scope.cancel()
    }
}
