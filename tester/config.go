package main

type TesterConfig struct {
	Tests []TestConfig `json:"tests"`
}

type TestConfig struct {
	Description string     `json:"description"`
	Steps       []TestStep `json:"steps"`
}

type TestStep struct {
	Action string                 `json:"action"`
	Args   map[string]interface{} `json:"args"`
}
