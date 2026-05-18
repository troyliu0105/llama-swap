package config

import (
	"fmt"
	"net/url"
	"time"
)

type PeerDictionaryConfig map[string]PeerConfig
type PeerConfig struct {
	Proxy            string            `yaml:"proxy"`
	ProxyURL         *url.URL          `yaml:"-"`
	ApiKey           string            `yaml:"apiKey"`
	Models           []string          `yaml:"models"`
	Filters          Filters           `yaml:"filters"`
	AddHeaders       map[string]string `yaml:"addHeaders"`
	RemoveHeaders    []string          `yaml:"removeHeaders"`
	MaxConcurrent    int               `yaml:"maxConcurrent"`
	QueueSize        int               `yaml:"queueSize"`
	QueueTimeout     time.Duration     `yaml:"queueTimeout"`
	RequestInterval  time.Duration     `yaml:"requestInterval"`
	StripV1Prefix    bool              `yaml:"stripV1Prefix"`
	PrefixPeerModels *bool             `yaml:"prefixPeerModels"`
	Timeout          time.Duration     `yaml:"timeout"`

	// Timeout settings for proxy connections
	Timeouts TimeoutsConfig `yaml:"timeouts"`
}

func (c *PeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPeerConfig PeerConfig
	defaults := rawPeerConfig{
		Proxy:         "",
		ApiKey:        "",
		Models:        []string{},
		Filters:       Filters{},
		AddHeaders:    map[string]string{},
		MaxConcurrent: 0,
		QueueSize:     32,
		QueueTimeout:  60 * time.Second,
		Timeout:       60 * time.Second,
		Timeouts: TimeoutsConfig{
			Connect:        30,
			KeepAlive:      30,
			ResponseHeader: 60,
			TLSHandshake:   10,
			ExpectContinue: 1,
			IdleConn:       90,
		},
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	if defaults.Proxy == "" {
		return fmt.Errorf("proxy is required")
	}

	parsedURL, err := url.Parse(defaults.Proxy)
	if err != nil {
		return fmt.Errorf("invalid peer proxy URL (%s): %w", defaults.Proxy, err)
	}
	defaults.ProxyURL = parsedURL

	if len(defaults.Models) == 0 {
		return fmt.Errorf("peer models can not be empty")
	}

	peerConfig := PeerConfig(defaults)
	if err := peerConfig.Validate(); err != nil {
		return err
	}

	*c = PeerConfig(defaults)
	return nil
}

func (c PeerConfig) Validate() error {
	return validatePeerConfigFields(c.MaxConcurrent, c.QueueSize, c.QueueTimeout, c.RequestInterval, c.Timeout)
}
