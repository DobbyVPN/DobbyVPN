package com.dobby.feature.main.ui

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.fragment.app.FragmentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts.StartActivityForResult
import androidx.lifecycle.lifecycleScope
import com.dobby.common.ui.theme.CkClientTheme
import com.dobby.navigation.App
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.initBiometricAuthenticationManager
import kotlinx.coroutines.launch
import org.koin.android.ext.android.inject

class DobbySocksActivity : FragmentActivity() {

    private lateinit var requestVpnPermissionLauncher: ActivityResultLauncher<Intent>

    private val permissionEventsChannel: PermissionEventsChannel by inject()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        initBiometricAuthenticationManager(this)

        initVpnPermissionLauncher()
        lifecycleScope.launch {
            permissionEventsChannel.checkPermissionsEvents.collect {
                checkVpnPermissionAndStart()
            }
        }
        setContent {
            CkClientTheme {
                App()
            }
        }
    }

    private fun checkVpnPermissionAndStart() {
        val vpnIntent = VpnService.prepare(this)
        if (vpnIntent != null) {
            requestVpnPermissionLauncher.launch(vpnIntent)
        } else {
            onPermissionGranted(isGranted = true)
        }
    }

    private fun initVpnPermissionLauncher() {
        requestVpnPermissionLauncher = registerForActivityResult(
            StartActivityForResult()
        ) { result -> onPermissionGranted(isGranted = result.resultCode == RESULT_OK) }
    }

    private fun onPermissionGranted(isGranted: Boolean) {
        lifecycleScope.launch { permissionEventsChannel.onPermissionGranted(isGranted) }
    }
}
