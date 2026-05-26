package com.dobby.feature.main.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable
fun LogMessageScreen(message: String) {
    val logMessage = LogMessage.parse(message)

    Text(
        buildAnnotatedString {
            withStyle(
                style = SpanStyle(
                    fontWeight = FontWeight.Bold,
                )
            ) {
                append("> ")
            }

            if (logMessage.time.isNotBlank()) {
                withStyle(
                    style = SpanStyle(
                        fontWeight = FontWeight.ExtraLight,
                    )
                ) {
                    append("[${logMessage.time}] ")
                }
            }

            withStyle(
                style = SpanStyle(
                    fontWeight = FontWeight.Bold,
                )
            ) {
                append("[${logMessage.level.name}] ")
            }

            withStyle(
                style = SpanStyle(
                    fontWeight = FontWeight.Bold,
                )
            ) {
                append("[${logMessage.category}] ")
            }

            withStyle(
                style = SpanStyle(
                    fontWeight = FontWeight.Normal,
                )
            ) {
                append(logMessage.message)
            }

            if (logMessage.isBackend) {
                withStyle(
                    style = SpanStyle(
                        fontWeight = FontWeight.Light,
                    )
                ) {
                    append(" [from go]")
                }
            }
        },
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 0.dp, horizontal = 4.dp),
        fontSize = 14.sp,
        fontFamily = FontFamily.Monospace,
        color = when (logMessage.level) {
            LogMessageLevel.DEBUG -> Color(0xFF999999)
            LogMessageLevel.INFO -> Color(0xFF000000)
            LogMessageLevel.WARN -> Color(0xFFCCCC00)
            LogMessageLevel.ERROR -> Color(0xFFFF0000)
        }
    )
}
