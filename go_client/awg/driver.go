package awg

type Driver interface {
	Connect() error
	Disconnect() error
	Refresh() error
}
