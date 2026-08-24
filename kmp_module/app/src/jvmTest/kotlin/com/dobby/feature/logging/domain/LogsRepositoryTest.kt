package com.dobby.feature.logging.domain

import java.nio.file.Files
import java.nio.file.attribute.PosixFilePermission
import kotlin.concurrent.thread
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import okio.Path.Companion.toPath
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class LogsRepositoryTest {
    @Test
    fun freshCurrentStorageLeavesLegacyLogUntouched() {
        val root = Files.createTempDirectory("dobby-log-fresh-current")
        val legacyDirectory = root.resolve(".myapp")
        val currentDirectory = root.resolve(".dobbyvpn")
        val legacy = legacyDirectory.resolve("app_logs.txt")
        val current = currentDirectory.resolve("app_logs.txt")
        try {
            Files.createDirectories(legacyDirectory)
            Files.writeString(legacy, "legacy remains")
            Files.createDirectories(currentDirectory)

            val previousHome = System.getProperty("user.home")
            try {
                System.setProperty("user.home", root.toString())
                assertEquals(current.toString(), provideLogFilePath().toString())
            } finally {
                if (previousHome == null) {
                    System.clearProperty("user.home")
                } else {
                    System.setProperty("user.home", previousHome)
                }
            }

            assertTrue(Files.exists(current))
            assertEquals("", Files.readString(current))
            assertEquals("legacy remains", Files.readString(legacy))
            try {
                assertEquals(
                    setOf(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE),
                    Files.getPosixFilePermissions(current),
                )
            } catch (_: UnsupportedOperationException) {
                // Windows uses its ACL path instead of POSIX mode bits.
            }
        } finally {
            Files.walk(root).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    @Test
    fun activeJvmClearRejectsFinalAlias() {
        val root = Files.createTempDirectory("dobby-log-clear-alias")
        val target = root.resolve("target.log")
        val link = root.resolve("app_logs.txt")
        try {
            Files.writeString(target, "must remain")
            Files.createSymbolicLink(link, target.fileName)

            assertFailsWith<IllegalStateException> {
                clearLogFile(link.toString().toPath(), fileSystem)
            }
            assertEquals("must remain", Files.readString(target))
        } finally {
            Files.walk(root).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    @Test
    fun activeLogCreationRejectsAliasedParent() {
        val root = Files.createTempDirectory("dobby-log-parent-alias")
        val target = root.resolve("real-current")
        val alias = root.resolve(".dobbyvpn")
        val previousHome = System.getProperty("user.home")
        try {
            Files.createDirectories(target)
            Files.createSymbolicLink(alias, target.fileName)
            System.setProperty("user.home", root.toString())

            assertFailsWith<LocalLogStorageInitializationException> {
                provideLogFilePath()
            }
            assertTrue(Files.notExists(target.resolve("app_logs.txt")))
        } finally {
            if (previousHome == null) {
                System.clearProperty("user.home")
            } else {
                System.setProperty("user.home", previousHome)
            }
            Files.walk(root).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    @Test
    fun groupWritableHomeIsRejectedBeforeCurrentStorageCreation() {
        val root = Files.createTempDirectory("dobby-log-group-writable-home")
        val posix = Files.getFileAttributeView(
            root,
            java.nio.file.attribute.PosixFileAttributeView::class.java,
        ) ?: return
        val originalPermissions = posix.readAttributes().permissions()
        val previousHome = System.getProperty("user.home")
        try {
            posix.setPermissions(originalPermissions + PosixFilePermission.GROUP_WRITE)
            System.setProperty("user.home", root.toString())

            assertFailsWith<LocalLogStorageInitializationException> {
                provideLogFilePath()
            }
            assertTrue(Files.notExists(root.resolve(".dobbyvpn")))
        } finally {
            posix.setPermissions(originalPermissions)
            if (previousHome == null) {
                System.clearProperty("user.home")
            } else {
                System.setProperty("user.home", previousHome)
            }
            Files.walk(root).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    @Test
    fun discoversAnExistingLogFromItsDirectoryWithoutRecreatingIt() {
        val directory = Files.createTempDirectory("dobby-existing-log")
        val log = directory.resolve("app_logs.txt")
        Files.writeString(log, "retained")

        try {
            ensureLogFileEntry(directory, log)

            assertEquals("retained", Files.readString(log))
        } finally {
            Files.deleteIfExists(log)
            Files.deleteIfExists(directory)
        }
    }

    @Test
    fun createsAMissingLogAndRejectsANonFileEntry() {
        val directory = Files.createTempDirectory("dobby-new-log")
        val log = directory.resolve("app_logs.txt")
        val target = directory.resolve("target.txt")

        try {
            ensureLogFileEntry(directory, log)
            assertTrue(Files.isRegularFile(log))

            Files.delete(log)
            Files.createDirectory(log)
            assertFailsWith<IllegalStateException> { ensureLogFileEntry(directory, log) }
            Files.delete(log)

            Files.writeString(target, "target")
            Files.createSymbolicLink(log, target.fileName)
            assertFailsWith<IllegalStateException> { ensureLogFileEntry(directory, log) }
            assertEquals("target", Files.readString(target))
        } finally {
            Files.deleteIfExists(log)
            Files.deleteIfExists(target)
            Files.deleteIfExists(directory)
        }
    }

    @Test
    fun parsesOnlyOneBoundedEffectiveWindowsUserSid() {
        assertEquals(
            "S-1-5-21-1000-2000-3000-4000",
            parseWindowsUserSid("\"WORKSTATION\\user\",\"S-1-5-21-1000-2000-3000-4000\"\r\n"),
        )
        listOf(
            "S-1-5-21-1000",
            "\"user\",\"S-1-5-21-1000\" trailing",
            "\"user\",\"S-1-5-21-1000\"\n\"other\",\"S-1-5-18\"",
            "\"user\",\"S-1-5-21-1000 & injected\"",
        ).forEach { value ->
            assertFailsWith<IllegalStateException> { parseWindowsUserSid(value) }
        }
    }

    @Test
    fun parsesLocalizedSystemAccountWithoutAcceptingControlOutput() {
        assertEquals("NT-AUTORITÄT\\SYSTEM", parseWindowsAccountName("NT-AUTORITÄT\\SYSTEM"))
        listOf(
            "SYSTEM",
            "NT AUTHORITY\\SYSTEM\nextra",
            "",
            "D\\${"x".repeat(256)}",
        ).forEach { value ->
            assertFailsWith<IllegalStateException> { parseWindowsAccountName(value) }
        }
    }

    @Test
    fun concurrent_writes_remain_complete_and_ui_is_human_readable() {
        val path = Files.createTempFile("dobby-jsonl", ".log")
        val repository = LogsRepository(path.toString().toPath())
        val workers = (0 until 80).map { index ->
            thread(start = true) {
                repository.writeEvent(
                    level = if (index % 2 == 0) LogLevel.TRACE else LogLevel.INFO,
                    source = "kmp",
                    event = "test.concurrent",
                    message = "status worker=$index",
                    fields = mapOf("worker" to index.toString()),
                )
            }
        }
        workers.forEach { it.join() }

        val raw = repository.readAllLogs()
        assertEquals(83, raw.size)
        raw.forEach { line ->
            val event = Json.parseToJsonElement(line).jsonObject
            assertEquals("dobby.log/v1", event["schema"]?.toString()?.trim('"'))
        }
        val ui = repository.readUILogs()
        assertEquals(LogsRepository.UI_TAIL_LINES, ui.size)
        assertTrue(ui.all { it.startsWith("[") })
        assertFalse(ui.any { it.startsWith("{") })
    }
}
