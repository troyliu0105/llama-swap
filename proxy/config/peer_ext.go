package config

import (
	"fmt"
	"net/url"
	"time"
)

type PeerDictionaryExtConfig map[string]ExtendedPeerConfig

type ExtendedPeerConfig struct {
	// Base fields (same as upstream PeerConfig)
	Proxy    string   `yaml:"proxy"`
	ProxyURL *url.URL `yaml:"-"`
	ApiKey   string   `yaml:"apiKey"`
	Models   []string `yaml:"models"`
	Filters  Filters  `yaml:"filters"`

	// Extended fields
	Headers          map[string]string `yaml:"headers"`
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

func (c *ExtendedPeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawExtendedPeerConfig ExtendedPeerConfig
	defaults := rawExtendedPeerConfig{
		Proxy:           "",
		ApiKey:          "",
		Models:          []string{},
		Filters:         Filters{},
		Headers:         map[string]string{},
		MaxConcurrent:   0,
		QueueSize:       32,
		QueueTimeout:    60 * time.Second,
		Timeout:         60 * time.Second,
		RequestInterval: 0,
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
		return fmt.Errorf("invalid peer proxy URL")
	}
	defaults.ProxyURL = parsedURL

	if len(defaults.Models) == 0 {
		return fmt.Errorf("peer models can not be empty")
	}

	*c = ExtendedPeerConfig(defaults)
	return nil
}
