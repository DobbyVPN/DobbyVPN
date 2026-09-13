package com.dobby.navigation

import androidx.compose.runtime.mutableStateListOf

enum class AppScreen {
    CONNECTION,
    SETTINGS,
}

/** Navigation state for the app's two root screens. */
class AppNavigationState {
    private val history = mutableStateListOf(AppScreen.CONNECTION)

    val currentScreen: AppScreen get() = history.last()
    val canGoBack: Boolean get() = history.size > 1

    fun select(screen: AppScreen) {
        if (screen == currentScreen) return
        history.add(screen)
    }

    /** Returns true when back selected the previous root screen. */
    fun goBack(): Boolean {
        if (!canGoBack) return false
        history.removeAt(history.lastIndex)
        return true
    }
}
