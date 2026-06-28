package com.dobby.cli

import kotlin.system.exitProcess

fun properExit(exitCode: ExitCode) {
    if (exitCode != ExitCode.OK)
        System.err.println(exitCode.description)
    exitProcess(exitCode.value)
}

fun printHelp(exitCode: ExitCode) {
    println(
        """
dobby - CLI tool for managing connections, logs, and status

USAGE:
    ./dobby <command> [options]

COMMANDS:
    --help
        Show this help message

    logs
        Manage and view logs

        USAGE:
            ./dobby logs -n [N]
                Show last N log entries

            ./dobby logs clear
                Clear all logs

    connect
        Establish a connection using a configuration file

        USAGE:
            ./dobby connect <config_path_or_url> [--skip-healthcheck]

        ARGS:
            <config_path_or_url>
                Path to configuration file. Can be remote file provided via URL.

        OPTIONS:
            --skip-healthcheck
                Skip healthcheck confirmation after connecting

    check-config
        Check every protocol profile from a configuration file

        USAGE:
            ./dobby check-config <config_path_or_url>

        ARGS:
            <config_path_or_url>
                Path to configuration file. Can be remote file provided via URL.

    disconnect
        Disconnect the current session

        USAGE:
            ./dobby disconnect

    status
        Show current system/connection status

        USAGE:
            ./dobby status [--json]

        OPTIONS:
            --json
                Output result in JSON format
                If not provided, output is printed in human-readable format
""".trimIndent()
    )

    properExit(exitCode)
}

fun runCliClient(args: Array<String>) {
    if (args.isEmpty()) {
        printHelp(ExitCode.INVALID_ARGS)
        return
    }

    val cliClient = CliClient()
    val options = args.drop(1)
    when (args[0]) {
        "--help" -> printHelp(ExitCode.OK)
        "logs" -> {
            val exitCode = cliClient.logs(options)
            properExit(exitCode)
        }

        "connect" -> {
            val exitCode = cliClient.connect(options)
            properExit(exitCode)
        }

        "check-config" -> {
            val exitCode = cliClient.checkConfig(options)
            properExit(exitCode)
        }

        "disconnect" -> {
            val exitCode = cliClient.disconnect(options)
            properExit(exitCode)
        }

        "status" -> {
            val exitCode = cliClient.status(options)
            properExit(exitCode)
        }

        else -> printHelp(ExitCode.INVALID_ARGS)
    }
}
