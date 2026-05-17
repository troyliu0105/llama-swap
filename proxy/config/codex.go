package config

import "fmt"

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
	Strategy string `yaml:"strategy"` // "sticky", "cache-hit" (default), or "round-robin"
}

func (c CodexConfig) Validate() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("codex accounts can not be empty")
	}
	for i, acct := range c.Accounts {
		if acct.Name == "" {
			return fmt.Errorf("codex account[%d] name can not be empty", i)
		}
	}
	if err := checkDuplicateAccountNames(c.Accounts); err != nil {
		return err
	}
	if err := c.LoadBalance.Validate(); err != nil {
		return err
	}
	return nil
}

func checkDuplicateAccountNames(accounts []CodexAccountConfig) error {
	seen := make(map[string]int)
	for i, acct := range accounts {
		if prev, exists := seen[acct.Name]; exists {
			return fmt.Errorf("codex account[%d] name %q duplicates account[%d]", i, acct.Name, prev)
		}
		seen[acct.Name] = i
	}
	return nil
}

func (c LoadBalanceConfig) Validate() error {
	switch c.Strategy {
	case "", "sticky", "cache-hit", "round-robin":
		return nil
	default:
		return fmt.Errorf("loadBalance.strategy must be one of: sticky, cache-hit, round-robin")
	}
}
