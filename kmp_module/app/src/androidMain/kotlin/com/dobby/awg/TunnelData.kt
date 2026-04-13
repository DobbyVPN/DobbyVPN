package com.dobby.awg

import com.dobby.feature.main.domain.AmneziaWGConfig

data class TunnelData(
    val name: String,
    val config: AmneziaWGConfig?,
    val state: TunnelState,
    val currentTunnelHandle: Int
)
