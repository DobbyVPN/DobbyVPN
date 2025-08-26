package com.dobby.feature.logging.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withLink
import androidx.compose.ui.unit.dp
import com.dobby.vpn.BuildConfig

@Composable
fun AboutScreen(
    modifier: Modifier = Modifier,
) {
    Column(modifier) {
        Text(
            text = "DobbyVPN",
            fontSize = MaterialTheme.typography.headlineMedium.fontSize,
            maxLines = 1,
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = 24.dp, end = 24.dp, top = 0.dp, bottom = 16.dp)
        )

        AboutRow("Version:", BuildConfig.VERSION_NAME)
        AboutRowLink(
            title = "Build commit:",
            value = BuildConfig.PROJECT_REPOSITORY_COMMIT,
            link = BuildConfig.PROJECT_REPOSITORY_COMMIT_LINK,
        )
    }
}

@Composable
fun AboutRow(
    title: String,
    value: String,
    modifier: Modifier = Modifier
) {
    Row(
        modifier = modifier.padding(horizontal = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Text(
            text = title,
            fontWeight = FontWeight.Bold,
        )
        Text(
            text = value,
            fontWeight = FontWeight.Normal,
        )
    }
}

@Composable
fun AboutRowLink(
    title: String,
    value: String,
    link: String,
    modifier: Modifier = Modifier
) {
    Row(
        modifier = modifier.padding(horizontal = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Text(
            text = title,
            fontWeight = FontWeight.Bold,
        )

        Text(
            buildAnnotatedString {
                withLink(
                    LinkAnnotation.Url(
                        url = link,
                        styles = TextLinkStyles(
                            style = SpanStyle(color = Color.Blue),
                        )
                    )
                ) {
                    append(value)
                }
            }
        )
    }
}
