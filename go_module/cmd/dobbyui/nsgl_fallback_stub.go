//go:build !darwin || ios || !cgo

package main

func installNSGLSoftwareFallback() error { return nil }
