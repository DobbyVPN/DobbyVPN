package outline

import "net"

type OutlineClient struct {
	driver Driver
}

func NewClient(transportConfig string) *OutlineClient {
	return &OutlineClient{driver: newDriver(transportConfig)}
}

func (c *OutlineClient) Connect() error {
	return c.driver.Connect()
}

func (c *OutlineClient) Disconnect() error {
	return c.driver.Disconnect()
}

func (c *OutlineClient) Read(buf []byte) (int, error) {
	return c.driver.Read(buf)
}

func (c *OutlineClient) Write(buf []byte) (int, error) {
	return c.driver.Write(buf)
}

func (c *OutlineClient) Refresh() error {
	return c.driver.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	return c.driver.GetServerIP()
}
