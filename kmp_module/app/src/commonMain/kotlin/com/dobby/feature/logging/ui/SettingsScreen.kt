package com.dobby.feature.logging.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withLink
import androidx.compose.ui.unit.dp
import com.dobby.feature.main.ui.AutomationSemantics
import com.dobby.vpn.BuildConfig

@Composable
fun SettingsScreen(
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .testTag(AutomationSemantics.SETTINGS_SCREEN)
            .semantics { contentDescription = AutomationSemantics.SETTINGS_SCREEN },
    ) {
        Text(
            text = "DobbyVPN",
            fontSize = MaterialTheme.typography.headlineMedium.fontSize,
            maxLines = 1,
            modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 0.dp, bottom = 16.dp)
        )
        Column(
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            AboutRow(
                "Version:",
                BuildConfig.VERSION_NAME,
                Modifier
                    .testTag(AutomationSemantics.BUILD_VERSION)
                    .semantics { contentDescription = AutomationSemantics.BUILD_VERSION },
            )
            AboutRowLink(
                title = "Build commit:",
                value = BuildConfig.PROJECT_REPOSITORY_COMMIT,
                link = BuildConfig.PROJECT_REPOSITORY_COMMIT_LINK,
                modifier = Modifier
                    .testTag(AutomationSemantics.BUILD_COMMIT)
                    .semantics { contentDescription = AutomationSemantics.BUILD_COMMIT },
            )
            Spacer(Modifier.padding(4.dp))
        }
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
