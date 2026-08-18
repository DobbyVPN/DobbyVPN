package com.dobby.domain

import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermission
import java.util.prefs.Preferences
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull

class DesktopSecretStorageTest {
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
