package awg

type AwgClient struct {
	driver Driver
}

func NewClient(config Config) (*AwgClient, error) {
	driver, err := newDriver(config)
	if err != nil {
		return nil, err
	}
	return &AwgClient{driver: driver}, nil
}

func (c *AwgClient) Connect() error {
	return c.driver.Connect()
}

func (c *AwgClient) Disconnect() error {
	return c.driver.Disconnect()
}

func (c *AwgClient) Refresh() error {
	return c.driver.Refresh()
}
