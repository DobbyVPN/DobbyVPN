package com.dobby.di

import org.koin.core.context.startKoin
import org.koin.core.module.Module
import org.koin.dsl.KoinAppDeclaration
import sharedModule

fun startDI(nativeModules: List<Module>, appDeclaration: KoinAppDeclaration = {}) {
    startKoin {
        appDeclaration()
        modules(nativeModules + sharedModule)
    }
}
