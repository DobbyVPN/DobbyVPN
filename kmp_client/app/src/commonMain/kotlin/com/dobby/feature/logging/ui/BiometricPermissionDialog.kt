package com.dobby.feature.logging.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.material3.*


@Composable
fun BiometricPermissionDialog(
    onAccept: () -> Unit,
    onDecline: () -> Unit
) {
    AlertDialog(
        onDismissRequest = onDecline,
        title = { Text("Biometrics and Location Access") },
        text = {
            Text(
                "You will now be asked to grant location permission. Please allow the request for this feature to work properly."
            )
        },
        confirmButton = {
            Text(
                "Sure",
                modifier = Modifier
                    .padding(8.dp)
                    .clickable { onAccept() }
            )
        },
        dismissButton = {
            Text(
                "I would rather not",
                modifier = Modifier
                    .padding(8.dp)
                    .clickable { onDecline() }
            )
        }
    )
}
