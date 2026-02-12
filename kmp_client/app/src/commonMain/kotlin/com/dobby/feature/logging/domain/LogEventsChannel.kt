package com.dobby.feature.logging.domain

import com.dobby.feature.logging.domain.LogsRepository.Companion.UI_TAIL_LINES
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow

class LogEventsChannel {

    private val _logEvents = MutableSharedFlow<String>(replay = UI_TAIL_LINES)

    val logEvents = _logEvents.asSharedFlow()

    suspend fun emitLog(message: String) {
        _logEvents.emit(message)
    }

    suspend fun emitLogs(messages: List<String>) {
        for (m in messages) {
            _logEvents.emit(m)
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    fun clear() {
        _logEvents.resetReplayCache()
    }
}
