package config

// CodexConfig holds Codex-specific peer configuration.
// Only used when peer Type is "codex".
type CodexConfig struct {
	Accounts    []CodexAccountConfig `yaml:"accounts"`
	LoadBalance LoadBalanceConfig    `yaml:"loadBalance"`
}

// CodexAccountConfig represents a named Codex account in the auth store.
type CodexAccountConfig struct {
	Name string `yaml:"name"`
}

// LoadBalanceConfig controls account selection strategy for Codex peers.
type LoadBalanceConfig struct {
	Strategy string `yaml:"strategy"` // "cache-hit" (default) or "round-robin"
}
