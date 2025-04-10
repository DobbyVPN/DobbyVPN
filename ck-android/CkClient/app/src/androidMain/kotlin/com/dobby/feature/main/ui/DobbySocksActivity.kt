package com.dobby.feature.main.ui

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts.StartActivityForResult
import androidx.lifecycle.lifecycleScope
import com.dobby.common.ui.theme.CkClientTheme
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.navigation.App
import kotlinx.coroutines.launch
import org.koin.androidx.viewmodel.ext.android.viewModel

class DobbySocksActivity : ComponentActivity() {

    private lateinit var requestVpnPermissionLauncher: ActivityResultLauncher<Intent>

    private val viewModel: MainViewModel by viewModel()

    private val logsViewModel: LogsViewModel by viewModel()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        initVpnPermissionLauncher()
        lifecycleScope.launch {
            viewModel.checkVpnPermissionEvents.collect { checkVpnPermissionAndStart() }
        }

        setContent {
            CkClientTheme {
                App(
                    mainViewModel = viewModel,
                    logsViewModel = logsViewModel,
                )
            }
        }
    }

    private fun checkVpnPermissionAndStart() {
        val vpnIntent = VpnService.prepare(this)
        if (vpnIntent != null) {
            requestVpnPermissionLauncher.launch(vpnIntent)
        } else {
            // not the best way to do it,
            viewModel.checkPermissionAndStartVpn(isGranted = true)
        }
    }

    private fun initVpnPermissionLauncher() {
        requestVpnPermissionLauncher = registerForActivityResult(
            StartActivityForResult()
        ) { result ->
            viewModel.checkPermissionAndStartVpn(isGranted = result.resultCode == RESULT_OK)
        }
    }
}
