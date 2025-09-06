package com.dobby.feature.main.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable
fun TagChip(
    tagText: String,
    color: Long
) {
    Box(
        modifier = Modifier
            .padding(4.dp)
            .background(Color(color), shape = RoundedCornerShape(24.dp))
            .padding(horizontal = 12.dp, vertical = 4.dp)
    ) {
        Text(
            text = tagText,
            style = TextStyle(
                color = Color.Black,
                fontSize = 14.sp
            ),
            modifier = Modifier.align(Alignment.Center)
        )
    }
}
