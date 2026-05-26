package com.dobby.cli

enum class ExitCode(val value: Int, val description: String) {
    OK(0, "OK"),
    INVALID_ARGS(1, "Invalid args"),
    PROGRAM_FAILED(2, "Program execution failed"),
    CONFIG_FORMAT_ERROR(3, "Invalid config provided"),
    TUNNEL_START_ERROR(4, "VPN start error"),
    HEALTHCHECK_CONFIG_ERROR(5, "Failed config VPN connection"),
}
