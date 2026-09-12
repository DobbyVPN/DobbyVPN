package com.dobby.domain

import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermissions
import java.util.prefs.Preferences
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class DesktopSecretStorageTest {
    @Test
    fun migratesLegacyStorageRootBeforeReadingTheRetainedSource() {
        val root = Files.createTempDirectory("dobby-secret-migration-test-")
        val legacy = root.resolve(".myapp").resolve("configs")
        val current = root.resolve(".dobbyvpn").resolve("configs")
        val node = Preferences.userRoot().node("dobby-secret-migration-test-${System.nanoTime()}")
        try {
            Files.createDirectories(legacy)
            Files.writeString(legacy.resolve("connection-url.txt"), "legacy-file-source")

            val repository = DobbyConfigsRepositoryImpl(node, current, legacy)

            assertEquals("legacy-file-source", repository.getConnectionURL())
            assertFalse(Files.exists(legacy.resolve("connection-url.txt")))
            assertOwnerOnly(current.resolve("connection-url.txt"))
        } finally {
            node.removeNode()
            Files.walk(root).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    @Test
    fun migratesLegacyPreferenceThenUsesOwnerOnlyFileWithoutFallback() {
        val directory = Files.createTempDirectory("dobby-secret-test-")
        val node = Preferences.userRoot().node("dobby-secret-test-${System.nanoTime()}")
        try {
            node.put("connectionURL", "legacy-secret")
            val repository = DobbyConfigsRepositoryImpl(node, directory)

            assertEquals("legacy-secret", repository.getConnectionURL())
            assertNull(node.get("connectionURL", null))
            assertOwnerOnly(directory.resolve("connection-url.txt"))

            // A stale plaintext preference is removed during the idempotent
            // next start and is never consulted by reads.
            node.put("connectionURL", "stale-plaintext")
            val restarted = DobbyConfigsRepositoryImpl(node, directory)
            assertEquals("legacy-secret", restarted.getConnectionURL())
            assertNull(node.get("connectionURL", null))
        } finally {
            node.removeNode()
            Files.walk(directory).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    @Test
    fun preservesConcreteWriteFailureAndRemovesTemporaryFile() {
        val directory = Files.createTempDirectory("dobby-secret-write-failure-test-")
        val node = Preferences.userRoot().node("dobby-secret-write-failure-test-${System.nanoTime()}")
        try {
            val destination = directory.resolve("connection-url.txt")
            Files.createDirectory(destination)
            Files.writeString(destination.resolve("existing"), "existing")
            val repository = DobbyConfigsRepositoryImpl(node, directory)

            val failure = try {
                repository.setConnectionURL("new-value")
                null
            } catch (error: Exception) {
                error
            }

            val concreteFailure = assertNotNull(failure as? java.nio.file.FileSystemException)
            assertTrue(concreteFailure.message?.contains("connection-url.txt") == true)
            Files.list(directory).use { files ->
                assertFalse(files.anyMatch { it.fileName.toString().startsWith(".dobby-source-") })
            }
        } finally {
            node.removeNode()
            try {
                Files.setPosixFilePermissions(
                    directory.resolve("connection-url.txt"),
                    PosixFilePermissions.fromString("rwx------"),
                )
            } catch (_: UnsupportedOperationException) {
                // Windows validates ACLs in the production implementation.
            }
            Files.walk(directory).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
            }
        }
    }

    private fun assertOwnerOnly(file: Path) {
        val view = Files.getFileAttributeView(file, java.nio.file.attribute.PosixFileAttributeView::class.java)
            ?: return // Windows validates ACLs in the production implementation.
        val permissions = view.readAttributes().permissions()
        assertFalse(permissions.contains(PosixFilePermission.GROUP_READ))
        assertFalse(permissions.contains(PosixFilePermission.OTHERS_READ))
        assertFalse(permissions.contains(PosixFilePermission.GROUP_WRITE))
        assertFalse(permissions.contains(PosixFilePermission.OTHERS_WRITE))
    }
}
