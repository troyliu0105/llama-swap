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
	Headers          map[string]string `yaml:"headers"`
	MaxConcurrent    int               `yaml:"maxConcurrent"`
	QueueSize        int               `yaml:"queueSize"`
	QueueTimeout     time.Duration     `yaml:"queueTimeout"`
	RequestInterval  time.Duration     `yaml:"requestInterval"`
	StripV1Prefix    bool              `yaml:"stripV1Prefix"`
	PrefixPeerModels *bool             `yaml:"prefixPeerModels"`
}

func (c *PeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPeerConfig PeerConfig
	defaults := rawPeerConfig{
		Proxy:         "",
		ApiKey:        "",
		Models:        []string{},
		Filters:       Filters{},
		Headers:       map[string]string{},
		MaxConcurrent: 0,
		QueueSize:     32,
		QueueTimeout:  60 * time.Second,
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

	*c = PeerConfig(defaults)
	return nil
}
