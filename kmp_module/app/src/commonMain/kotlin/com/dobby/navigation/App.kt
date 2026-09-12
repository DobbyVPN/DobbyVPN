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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.dobby.feature.logging.ui.SettingsScreen
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.main.ui.ConnectionScreen
import com.dobby.feature.main.ui.AutomationSemantics
import com.dobby.util.koinViewModel

@Composable
fun App(modifier: Modifier = Modifier) {
    val mainViewModel: MainViewModel = koinViewModel()
    val logsViewModel: LogsViewModel = koinViewModel()

    MaterialTheme(
        colorScheme = lightColorScheme(
            background = Color.White,
            surface = Color.White
        )
    ) {
        val navController = rememberNavController()
        val keyboardController = LocalSoftwareKeyboardController.current

        Scaffold(
            modifier = modifier
                .pointerInput(Unit) {
                    detectTapGestures(onTap = { keyboardController?.hide() })
                },
            bottomBar = {
                BottomBar(navController)
            },
            content = { innerPadding ->
                NavHost(
                    modifier = Modifier.padding(innerPadding),
                    navController = navController,
                    startDestination = MainScreen
                ) {
                    composable<MainScreen> {
                        ConnectionScreen(mainViewModel = mainViewModel, logsViewModel = logsViewModel)
                    }
                    composable<SettingsScreen> {
                        SettingsScreen()
                    }
                }
            }
        )
    }
}

@Composable
private fun BottomBar(
    navController: NavHostController
) {
    var selectedItem by remember { mutableIntStateOf(0) }
    val items = listOf("Connection", "Settings")
    val screens = listOf(MainScreen, SettingsScreen)
    val selectedIcons = listOf(Icons.Filled.Home, Icons.Default.Settings)

    NavigationBar {
        items.forEachIndexed { index, item ->
            NavigationBarItem(
                modifier = Modifier
                    .testTag(if (index == 0) AutomationSemantics.CONNECTION_NAV else AutomationSemantics.SETTINGS_NAV)
                    .semantics {
                        contentDescription = if (index == 0) {
                            AutomationSemantics.CONNECTION_NAV
                        } else {
                            AutomationSemantics.SETTINGS_NAV
                        }
                    },
                icon = {
                    Icon(
                        selectedIcons[index],
                        contentDescription = null
                    )
                },
                label = { Text(item) },
                selected = selectedItem == index,
                onClick = {
                    selectedItem = index
                    navController.navigate(screens[index]) {
                        launchSingleTop = true
                        restoreState = true
                    }
                }
            )
        }
    }
}
