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

	// Peer type: "" (default) or "codex"
	Type  string       `yaml:"type"`
	Codex *CodexConfig `yaml:"codex"`
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
		Type:            "",
		Codex:           nil,
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

	// Validate peer type
	switch defaults.Type {
	case "", "codex":
	default:
		return fmt.Errorf("unknown peer type: %s", defaults.Type)
	}

	// For codex peers, proxy defaults to https://chatgpt.com
	if defaults.Type == "codex" {
		if defaults.Proxy == "" {
			defaults.Proxy = "https://chatgpt.com"
		}
	} else if defaults.Proxy == "" {
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

	// Codex-specific validation
	if defaults.Type == "codex" {
		if defaults.Codex == nil {
			return fmt.Errorf("codex config is required when type is codex")
		}
		if len(defaults.Codex.Accounts) == 0 {
			return fmt.Errorf("codex accounts can not be empty")
		}
		for i, acct := range defaults.Codex.Accounts {
			if acct.Name == "" {
				return fmt.Errorf("codex account[%d] name can not be empty", i)
			}
		}
		if defaults.Codex.LoadBalance.Strategy == "" {
			defaults.Codex.LoadBalance.Strategy = "cache-hit"
		}
	}

	peerConfig := ExtendedPeerConfig(defaults)
	if err := peerConfig.Validate(); err != nil {
		return err
	}

	*c = peerConfig
	return nil
}

func (c ExtendedPeerConfig) Validate() error {
	if err := validatePeerConfigFields(c.MaxConcurrent, c.QueueSize, c.QueueTimeout, c.RequestInterval, c.Timeout); err != nil {
		return err
	}
	if c.Type == "codex" {
		if c.Codex == nil {
			return fmt.Errorf("codex config is required when type is codex")
		}
		if err := c.Codex.Validate(); err != nil {
			return err
		}
	}
	return nil
}
