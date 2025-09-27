package cloak

import ("github.com/cbeuw/Cloak/exported_client"
	"github.com/BurntSushi/toml"
)
type RawConfig = exported_client.Config

func ParseCloakTOML(tomlStr string, out *RawConfig) error {
	var wrapper struct {
		Cloak RawConfig `toml:"cloak"`
	}
	if _, err := toml.Decode(tomlStr, &wrapper); err != nil {
		return err
	}
	*out = wrapper.Cloak
	return nil
}
