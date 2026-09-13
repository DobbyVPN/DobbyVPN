package com.dobby.navigation

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.lifecycle.viewmodel.compose.viewModel
import com.dobby.AppDependencies
import com.dobby.feature.main.ui.AutomationSemantics
import com.dobby.feature.main.ui.ConnectionScreen
import com.dobby.feature.logging.ui.SettingsScreen as SettingsScreenContent

@Composable
fun App(
    dependencies: AppDependencies,
    modifier: Modifier = Modifier,
) {
    val mainViewModel = viewModel { dependencies.createMainViewModel() }
    val logsViewModel = viewModel { dependencies.createLogsViewModel() }
    val navigation = dependencies.navigation
    val screen = navigation.currentScreen
    val stateHolder = rememberSaveableStateHolder()
    val keyboardController = LocalSoftwareKeyboardController.current

    MaterialTheme(
        colorScheme = lightColorScheme(
            background = Color.White,
            surface = Color.White,
        ),
    ) {
        Scaffold(
            modifier = modifier
                .pointerInput(Unit) {
                    detectTapGestures(onTap = { keyboardController?.hide() })
                },
            bottomBar = { BottomBar(navigation) },
        ) { innerPadding ->
            stateHolder.SaveableStateProvider(screen.name) {
                when (screen) {
                    AppScreen.CONNECTION -> ConnectionScreen(
                        mainViewModel = mainViewModel,
                        logsViewModel = logsViewModel,
                        modifier = Modifier.padding(innerPadding),
                    )
                    AppScreen.SETTINGS -> SettingsScreenContent(
                        modifier = Modifier.padding(innerPadding),
                    )
                }
            }
        }
    }
}

@Composable
private fun BottomBar(navigation: AppNavigationState) {
    val screen = navigation.currentScreen
    val items = listOf(
        Triple("Connection", AppScreen.CONNECTION, Icons.Filled.Home),
        Triple("Settings", AppScreen.SETTINGS, Icons.Default.Settings),
    )

    NavigationBar {
        items.forEach { (label, target, icon) ->
            val tag = if (target == AppScreen.CONNECTION) {
                AutomationSemantics.CONNECTION_NAV
            } else {
                AutomationSemantics.SETTINGS_NAV
            }
            NavigationBarItem(
                modifier = Modifier
                    .testTag(tag)
                    .semantics { contentDescription = tag },
                icon = { Icon(icon, contentDescription = null) },
                label = { Text(label) },
                selected = screen == target,
                onClick = { navigation.select(target) },
            )
        }
    }
}
