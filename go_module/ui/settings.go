package ui

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Version and Commit are replaced by release builds with -ldflags. Keeping
// the defaults useful makes locally injected prototype binaries self-
// describing instead of showing an empty settings page.
var (
	Version = "development"
	Commit  = "unknown"
)

type SettingsView struct {
	Back    *widget.Button
	Version *widget.Label
	Commit  *widget.Hyperlink
	root    fyne.CanvasObject
}

func NewSettingsView() *SettingsView {
	view := &SettingsView{
		Back:    widget.NewButton("Back", nil),
		Version: widget.NewLabel("Version: " + Version),
		Commit:  widget.NewHyperlink("Source commit: "+Commit, sourceURL(Commit)),
	}
	view.root = container.NewVBox(view.Back, view.Version, view.Commit)
	return view
}

func (v *SettingsView) Content() fyne.CanvasObject { return v.root }

func sourceURL(commit string) *url.URL {
	value, _ := url.Parse("https://github.com/DobbyVPN/DobbyVPN/tree/" + commit)
	return value
}
