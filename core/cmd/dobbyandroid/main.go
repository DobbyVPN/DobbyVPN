//go:build android

// Package main builds the Android Go session backend as a shared library.
// The Kotlin Activity owns the UI and the Android VPN lifecycle boundary.
package main

import _ "core/android_exports"

func main() {}
