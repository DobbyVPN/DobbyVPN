package com.dobby.cli

enum class ExitCode(val value: Int) {
    OK(0),
    INVALID_ARGS(1),
    PROGRAM_FAILED(2),
}
