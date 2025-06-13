package com.dobby.navigation

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.dobby.feature.diagnostic.ui.DiagnosticScreen
import com.dobby.feature.logging.ui.AboutScreen
import com.dobby.feature.logging.ui.LogScreen
import com.dobby.feature.logging.ui.SettingsScreen
import com.dobby.feature.main.ui.AwgScreen
import com.dobby.feature.main.ui.DobbySocksScreen
import com.dobby.util.koinViewModel

@Composable
fun App(modifier: Modifier = Modifier) {
    val navController = rememberNavController()
    val keyboardController = LocalSoftwareKeyboardController.current

    Scaffold(
        modifier = modifier
            .pointerInput(Unit) {
                detectTapGestures(onTap = { keyboardController?.hide() })
            },
        bottomBar = {
            BottomBar(navController::navigate)
        },
        content = { innerPadding ->
            NavHost(
                modifier = Modifier.padding(innerPadding),
                navController = navController,
                startDestination = MainScreen
            ) {
                composable<MainScreen> {
                    DobbySocksScreen(viewModel = koinViewModel())
                }
                composable<AmneziaWGScreen> {
                    AwgScreen(viewModel = koinViewModel())
                }
                composable<DiagnosticsScreen> {
                    DiagnosticScreen(viewModel = koinViewModel())
                }
                composable<LogsScreen> {
                    LogScreen(viewModel = koinViewModel())
                }
                composable<SettingsScreen> {
                    SettingsScreen(onNavigate = navController::navigate)
                }
                composable<AboutScreen> {
                    AboutScreen()
                }
            }
        }
    )
}

@Composable
private fun BottomBar(onNavigate: (Any) -> Unit = {}) {
    var selectedItem by remember { mutableIntStateOf(0) }
    val items = listOf("Outline", "AmneziaWG", "Settings")
    val screens = listOf(MainScreen, AmneziaWGScreen, SettingsScreen)
    val selectedIcons =
        listOf(Icons.Filled.Home, Icons.Filled.Favorite, Icons.Default.Settings)

    NavigationBar {
        items.forEachIndexed { index, item ->
            NavigationBarItem(
                icon = {
                    Icon(
                        selectedIcons[index],
                        contentDescription = item
                    )
                },
                label = { Text(item) },
                selected = selectedItem == index,
                onClick = {
                    selectedItem = index
                    onNavigate.invoke(screens[index])
                }
            )
        }
    }
}
