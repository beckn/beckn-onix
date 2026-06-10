package plugin

type PublisherCfg struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

type ValidatorCfg struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

type Config struct {
	ID     string            `yaml:"id"`
	Config map[string]string `yaml:"config"`
}

type ManagerConfig struct {
	Root           string               `yaml:"root"`
	RemoteRoot     string               `yaml:"remoteRoot"`
	BecknConstants *BecknConstantsConfig `yaml:"becknConstants,omitempty"`
}

// BecknConstantsConfig controls the beckn constants loader inside the manager.
type BecknConstantsConfig struct {
	DisableRemoteRefresh bool `yaml:"disableRemoteRefresh"`
}
