package com.dobby.di

import com.dobby.feature.main.presentation.MainViewModel
import org.koin.mp.KoinPlatform

fun getMainViewModel(): MainViewModel = KoinPlatform.getKoin().get()
