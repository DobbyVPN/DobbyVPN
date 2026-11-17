//go:build !(windows || android || ios)

package executor

// startSystemEvents reports network changes to the core for as long as the
// process runs. A wake-up without a network change is not reported separately:
// a polling timer would bring back idle wake-ups, and the link and address
// changes that follow a wake-up normally arrive anyway.
func startSystemEvents() {
	go watchNetworkChanges(newNetworkEvents())
}
