package com.dobby.feature.netcheck.domain

import com.dobby.feature.logging.domain.fileSystem
import okio.BufferedSource
import okio.FileSystem
import okio.Path
import okio.buffer
import okio.use

expect val fileSystem: FileSystem
expect fun provideNetCheckConfigPath(): Path

class NetCheckRepository(
    private val configPath: Path = provideNetCheckConfigPath(),
) {

    init {
        if (!fileSystem.exists(configPath)) {
            fileSystem.sink(configPath).buffer().use { }
        }
    }

    fun setConfig(config: String) =
        try {
            fileSystem.sink(configPath).buffer().use { sink -> sink.writeUtf8(config) }
        } catch (e: Exception) {
            e.printStackTrace()
        }

    fun getConfig(): String =
        try {
            fileSystem
                .source(configPath)
                .buffer()
                .use(BufferedSource::readUtf8)
        } catch (e: Exception) {
            e.printStackTrace()
            ""
        }

    fun getConfigPath(): String = configPath.toString()
}
