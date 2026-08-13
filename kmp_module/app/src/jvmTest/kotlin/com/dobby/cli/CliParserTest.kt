package com.dobby.cli

import com.dobby.feature.logging.domain.LocalLogStorageInitializationException
import java.nio.file.AccessDeniedException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class CliParserTest {
    @Test
    fun localStorageInitializationFailureReportsOnlyBoundedStageAndType() {
        val error = LocalLogStorageInitializationException(
            "service.file_acl",
            AccessDeniedException("C:\\Users\\sensitive\\app_logs.txt"),
        )

        val message = formatCliInitializationFailure(error)

        assertEquals(
            "DobbyVPN local log storage initialization failed " +
                "stage=service.file_acl failureType=AccessDeniedException",
            message,
        )
        assertFalse(message.contains("sensitive"))
        assertFalse(message.contains("Users"))
    }

}
