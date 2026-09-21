package ui

import (
	"log"

	"fyne.io/fyne/v2"
)

// nativeWindowTitlePublisher keeps the last semantic title only after one
// publication attempt. A pre-Show native context is intentionally not used:
// the ordinary Fyne title is installed before Show, and Application.run makes
// the first native publication after Show. Later presentation flushes invoke
// this publisher, but equal statuses do not emit duplicate AppKit/AX writes.
// A failed attempt is logged and remembered; retry loops would hide a broken
// native bridge and make the real-window check less deterministic.
type nativeWindowTitlePublisher struct {
	window  fyne.Window
	publish func(fyne.Window, string) error
	title   string
	done    bool
}

func newNativeWindowTitlePublisher(window fyne.Window) *nativeWindowTitlePublisher {
	return &nativeWindowTitlePublisher{window: window, publish: publishNativeWindowTitle}
}

func (p *nativeWindowTitlePublisher) Publish(status string) {
	title := nativeWindowTitle(status)
	if p.done && p.title == title {
		return
	}
	if err := p.publish(p.window, title); err != nil {
		log.Printf("native window title publication failed: %v", err)
	}
	p.title = title
	p.done = true
}

// publishNativeWindowTitle keeps the shared UI's semantic status in the
// normal Fyne window title and gives platforms with a stale accessibility
// metadata bridge one product-owned native publication hook. The hook is
// intentionally outside the service/state model: Fyne remains the window
// owner and the native call is only an output publication for automation.
func publishNativeWindowTitle(window fyne.Window, title string) error {
	window.SetTitle(title)
	return publishPlatformNativeWindowTitle(window, title)
}
