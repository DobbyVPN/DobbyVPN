package com.dobby.ui

import androidx.compose.ui.window.ComposeUIViewController
import com.dobby.AppDependencies
import com.dobby.navigation.App

fun MainViewController(dependencies: AppDependencies) = ComposeUIViewController {
    App(dependencies)
}
