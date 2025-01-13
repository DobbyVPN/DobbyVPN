package com.dobby.awg

import com.dobby.awg.config.Config

data class TunnelData(
    val name: String,
    val config: Config?,
    val state: TunnelState,
    val currentTunnelHandle: Int
)
