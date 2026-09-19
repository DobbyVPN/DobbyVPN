package ui

import (
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Version and Commit are replaced by release builds with -ldflags. Release
// drivers source the version from the repository VERSION file; these defaults
// are only for locally built binaries and never define a release version.
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
	version := displayMetadata(Version, "development")
	commit := displayMetadata(Commit, "unknown")
	view := &SettingsView{
		Back:    widget.NewButton("Back", nil),
		Version: widget.NewLabel("Version: " + version),
		Commit:  widget.NewHyperlink("Source commit: "+commit, sourceURL(commit)),
	}
	view.root = container.NewVBox(view.Back, view.Version, view.Commit)
	return view
}

func (v *SettingsView) Content() fyne.CanvasObject { return v.root }

func displayMetadata(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func sourceURL(commit string) *url.URL {
	commit = strings.TrimSpace(commit)
	if commit == "" || commit == "unknown" || commit == "development" {
		return nil
	}
	value, err := url.Parse("https://github.com/DobbyVPN/DobbyVPN/tree/" + url.PathEscape(commit))
	if err != nil {
		return nil
	}
	return value
}
