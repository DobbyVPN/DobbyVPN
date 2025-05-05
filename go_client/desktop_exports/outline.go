package main

import "go_client/outline"

type OutlineClient struct {
	*outline.OutlineClient
}

func (c *OutlineClient) Connect() error {
	return c.OutlineClient.Connect()
}

func (c *OutlineClient) Disconnect() error {
	return c.OutlineClient.Disconnect()
}

//func (c *OutlineClient) GetServerIP() net.IP {
//	return c.OutlineClient.GetServerIP()
//}

func (c *OutlineClient) Read() ([]byte, error) {
	return c.OutlineClient.Read()
}

func (c *OutlineClient) Write(buf []byte) (int, error) {
	return c.OutlineClient.Write(buf)
}

func NewOutlineClient(transportConfig string) *OutlineClient {
	cl := outline.NewClient(transportConfig)
	return &OutlineClient{OutlineClient: cl}
}
