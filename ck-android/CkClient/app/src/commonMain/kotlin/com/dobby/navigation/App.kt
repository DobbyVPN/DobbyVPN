package com.dobby.navigation

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.dobby.feature.logging.ui.LogScreen
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.main.ui.DobbySocksScreen

@Composable
fun App(
    modifier: Modifier = Modifier,
    mainViewModel: MainViewModel,
) {
    val navController = rememberNavController()

    NavHost(
        navController = navController,
        startDestination = MainScreen
    ) {
        composable<MainScreen> {
            DobbySocksScreen(
                modifier,
                navController,
                mainViewModel,
            )
        }
        composable<AmneziaWGScreen> {
            AmneziaWGApp(
                modifier,
                navController,
                mainViewModel,
            )
        }
        composable<LogsScreen> {
            LogScreen()
        }
    }
}
