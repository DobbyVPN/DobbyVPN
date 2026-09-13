package com.dobby.navigation

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class AppNavigationStateTest {
    @Test
    fun selecting_root_screens_updates_selection_and_back_traverses_history() {
        val navigation = AppNavigationState()

        assertEquals(AppScreen.CONNECTION, navigation.currentScreen)
        assertFalse(navigation.canGoBack)
        assertFalse(navigation.goBack())

        navigation.select(AppScreen.SETTINGS)
        assertEquals(AppScreen.SETTINGS, navigation.currentScreen)
        assertTrue(navigation.canGoBack)

        navigation.select(AppScreen.CONNECTION)
        navigation.select(AppScreen.SETTINGS)

        assertTrue(navigation.goBack())
        assertEquals(AppScreen.CONNECTION, navigation.currentScreen)
        assertTrue(navigation.goBack())
        assertEquals(AppScreen.SETTINGS, navigation.currentScreen)
        assertTrue(navigation.goBack())
        assertEquals(AppScreen.CONNECTION, navigation.currentScreen)
        assertFalse(navigation.canGoBack)
        assertFalse(navigation.goBack())
    }

    @Test
    fun selecting_the_current_screen_does_not_create_back_history() {
        val navigation = AppNavigationState()

        navigation.select(AppScreen.CONNECTION)

        assertEquals(AppScreen.CONNECTION, navigation.currentScreen)
        assertFalse(navigation.canGoBack)
    }
}
