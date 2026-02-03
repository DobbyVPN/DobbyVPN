package com.dobby.navigation

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Favorite
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
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.dobby.feature.authentication.domain.HideConfigsManager
import com.dobby.feature.authentication.presentation.AuthenticationSettingsViewModel
import com.dobby.feature.logging.ui.SettingsScreen
import com.dobby.feature.authentication.ui.AuthenticationScreen
import com.dobby.feature.authentication.ui.LoadingScreen
import com.dobby.feature.authentication.ui.WebViewScreen
import com.dobby.feature.logging.presentation.SettingsViewModel
import com.dobby.feature.main.ui.DobbySocksScreen
import com.dobby.util.koinViewModel

@Composable
fun App(modifier: Modifier = Modifier) {
    val settingsViewModel: SettingsViewModel = koinViewModel()
    val authenticationSettingsViewModel: AuthenticationSettingsViewModel = koinViewModel()
    val tryEnableHideConfigsStatus by authenticationSettingsViewModel.tryEnableHideConfigsStatus.collectAsState()

    MaterialTheme(
        colorScheme = lightColorScheme(
            background = Color.White,
            surface = Color.White
        )
    ) {
        val navController = rememberNavController()
        val keyboardController = LocalSoftwareKeyboardController.current
        HideConfigsManager.authStatus = HideConfigsManager.AuthStatus.NONE
        val authState by HideConfigsManager.authState.collectAsState()

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
                        if (authState == HideConfigsManager.AuthStatus.NONE) {
                            AuthenticationScreen(
                                screen = MainScreen,
                                navController = navController
                            )
                        } else {
                            DobbySocksScreen()
                        }
                    }
                    composable<SettingsScreen> {
                        if (tryEnableHideConfigsStatus == HideConfigsManager.TryEnableHideConfigsResult.SUCCESS &&
                            authState == HideConfigsManager.AuthStatus.NONE
                            ) {
                                AuthenticationScreen(
                                    screen = SettingsScreen,
                                    navController = navController
                                )
                        } else {
                            HideConfigsManager.authStatus = HideConfigsManager.AuthStatus.SUCCESS
                            SettingsScreen(
                                authenticationSettingsViewModel = authenticationSettingsViewModel,
                                settingsViewModel = settingsViewModel,
                            )
                        }
                    }
                    composable<WebViewScreen> {
                        if (authState == HideConfigsManager.AuthStatus.NONE) {
                            AuthenticationScreen(
                                screen = MainScreen,
                                navController = navController
                            )
                        } else {
                            WebViewScreen()
                        }
                    }
                    composable<LoadingScreen> {
                        LoadingScreen()
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
    val selectedIcons =
        listOf(Icons.Filled.Home, Icons.Filled.Favorite, Icons.Default.Settings)

    val authState by HideConfigsManager.authState.collectAsState()

    if (authState == HideConfigsManager.AuthStatus.SUCCESS) {
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
                        navController.navigate(screens[index]) {
                            launchSingleTop = true
                            restoreState = true
                        }
                    }
                )
            }
        }
    }
}
