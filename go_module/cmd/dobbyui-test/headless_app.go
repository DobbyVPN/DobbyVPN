//go:build !(android || ios)

package main

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// serializedTestDriver gives the production UI a single execution context
// when it is exercised with Fyne's in-memory driver. The Fyne test driver runs
// DoFromGoroutine callbacks immediately, so background presentation callbacks
// would otherwise race a test command that taps or edits a widget.
type serializedTestDriver struct {
	fyne.Driver
	cond    *sync.Cond
	queue   []func()
	closing bool
	stopped chan struct{}
}

func newSerializedTestDriver(base fyne.Driver) *serializedTestDriver {
	driver := &serializedTestDriver{
		Driver:  base,
		stopped: make(chan struct{}),
	}
	driver.cond = sync.NewCond(&sync.Mutex{})
	go driver.run()
	return driver
}

func (d *serializedTestDriver) run() {
	defer close(d.stopped)
	for {
		d.cond.L.Lock()
		for len(d.queue) == 0 && !d.closing {
			d.cond.Wait()
		}
		if len(d.queue) == 0 && d.closing {
			d.cond.L.Unlock()
			return
		}
		fn := d.queue[0]
		d.queue[0] = nil
		d.queue = d.queue[1:]
		d.cond.L.Unlock()
		fn()
	}
}

func (d *serializedTestDriver) DoFromGoroutine(fn func(), wait bool) {
	var done chan struct{}
	d.cond.L.Lock()
	if d.closing {
		d.cond.L.Unlock()
		return
	}
	if wait {
		done = make(chan struct{})
		d.queue = append(d.queue, func() {
			defer close(done)
			fn()
		})
	} else {
		d.queue = append(d.queue, fn)
	}
	d.cond.Signal()
	d.cond.L.Unlock()
	if wait {
		<-done
	}
}

func (d *serializedTestDriver) stop() {
	d.cond.L.Lock()
	d.closing = true
	d.cond.Broadcast()
	d.cond.L.Unlock()
}

func (d *serializedTestDriver) close() {
	d.stop()
	<-d.stopped
}

type serializedTestApp struct {
	fyne.App
	driver *serializedTestDriver
}

func newSerializedTestApp() *serializedTestApp {
	base := test.NewApp()
	application := &serializedTestApp{
		App:    base,
		driver: newSerializedTestDriver(base.Driver()),
	}
	fyne.SetCurrentApp(application)
	return application
}

func (a *serializedTestApp) Driver() fyne.Driver { return a.driver }

func (a *serializedTestApp) Quit() {
	a.App.Quit()
	a.driver.stop()
}

func (a *serializedTestApp) close() {
	a.Quit()
	a.driver.close()
}
