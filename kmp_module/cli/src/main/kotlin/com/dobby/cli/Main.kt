package com.dobby.cli

import kotlin.system.exitProcess

fun printHelp(statusCode: Int) {
    println("""
Usage:
  program_name <action> <config_path>
  program_name --help

Description:
  Just a small CLI client.

Arguments:
  <action>        Required. Specifies the operation to perform.
                  Must be one of:
                    connect      Establish a connection
                    disconnect   Terminate an existing connection
                    logs         Lock terminal and print logs in infinite loop

  <config_path>   Required if action = connect. Path to configuration file.

Options:
  --help          Show this help message and exit

Examples:
  program_name connect /path/to/config.toml
  program_name disconnect /path/to/config.toml
  program_name logs
  program_name --help

Errors:
  - Missing or invalid <action> argument
  - Configuration file not found or not readable
""".trimIndent())

    exitProcess(statusCode)
}

fun main(args: Array<String>) {
    println(args.joinToString(", "))
    if (args.isNotEmpty()) {
        val cliClient = CliClient()
        when (args[0]) {
            "--help" -> printHelp(0)
            "connect" -> {
                if (args.size == 2) {
                    cliClient.connect(args[1])
                } else {
                    printHelp(1)
                }
            }

            "disconnect" -> {
                if (args.size == 1) {
                    cliClient.disconnect()
                } else {
                    printHelp(1)
                }
            }

            "logs" -> {
                if (args.size == 1) {
                    cliClient.logs()
                } else {
                    printHelp(1)
                }
            }

            else -> printHelp(1)
        }
    } else {
        printHelp(1)
    }
}
