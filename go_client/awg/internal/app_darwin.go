//go:build darwin

package internal

type App struct {
}

// NewApp creates a new App using a tunnel name and its config
func NewApp(tunnel, config string) (*App, error) {
	app := &App{}

	return app, nil
}

func (a *App) Run() error {
	return nil
}

func (a *App) Stop() {
}
