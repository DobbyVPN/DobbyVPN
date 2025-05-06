package main

import "go_client/outline"

type OutlineClient struct {
	*outline.OutlineClient
}

//export Connect
func (c *OutlineClient) Connect() error {
	return c.OutlineClient.Connect()
}

//export Disconnect
func (c *OutlineClient) Disconnect() error {
	return c.OutlineClient.Disconnect()
}

//func (c *OutlineClient) GetServerIP() net.IP {
//	return c.OutlineClient.GetServerIP()
//}

//export Read
func (c *OutlineClient) Read() ([]byte, error) {
	return c.OutlineClient.Read()
}

//export Write
func (c *OutlineClient) Write(buf []byte) (int, error) {
	return c.OutlineClient.Write(buf)
}

//export NewOutlineClient
func NewOutlineClient(transportConfig string) *OutlineClient {
	cl := outline.NewClient(transportConfig)
	return &OutlineClient{OutlineClient: cl}
}
