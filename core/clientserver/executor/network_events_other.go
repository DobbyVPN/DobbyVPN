//go:build !linux && !darwin && !(windows || android || ios)

package executor

func watchNetworkChanges(func(string)) {}
