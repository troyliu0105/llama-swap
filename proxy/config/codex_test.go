package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexConfig_ValidMinimal(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts:
        - name: personal
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	peer, ok := config.Peers["my-codex"]
	require.True(t, ok)
	assert.Equal(t, "codex", peer.Type)
	assert.NotNil(t, peer.Codex)
	require.Len(t, peer.Codex.Accounts, 1)
	assert.Equal(t, "personal", peer.Codex.Accounts[0].Name)
}

func TestCodexConfig_MissingAccounts(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts: []
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex accounts can not be empty")
}

func TestCodexConfig_EmptyAccountName(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts:
        - name: ""
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name can not be empty")
}

func TestCodexConfig_DefaultProxy(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts:
        - name: personal
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	peer := config.Peers["my-codex"]
	assert.Equal(t, "https://chatgpt.com", peer.Proxy)
	assert.Equal(t, "chatgpt.com", peer.ProxyURL.Host)
}

func TestCodexConfig_DefaultStrategy(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts:
        - name: personal
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	peer := config.Peers["my-codex"]
	assert.Equal(t, "cache-hit", peer.Codex.LoadBalance.Strategy)
}

func TestCodexConfig_UnknownType(t *testing.T) {
	content := `
peers:
  bad-peer:
    type: unknown-type
    proxy: http://example.com
    models:
      - some-model
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown peer type")
}

func TestCodexConfig_MissingModels(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models: []
    codex:
      accounts:
        - name: personal
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peer models can not be empty")
}

func TestCodexConfig_MultipleAccounts(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
      - codex-large
    codex:
      accounts:
        - name: personal
        - name: work
      loadBalance:
        strategy: round-robin
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	peer := config.Peers["my-codex"]
	assert.Equal(t, "codex", peer.Type)
	assert.NotNil(t, peer.Codex)
	require.Len(t, peer.Codex.Accounts, 2)
	assert.Equal(t, "personal", peer.Codex.Accounts[0].Name)
	assert.Equal(t, "work", peer.Codex.Accounts[1].Name)
	assert.Equal(t, "round-robin", peer.Codex.LoadBalance.Strategy)
	assert.Equal(t, []string{"codex-mini", "codex-large"}, peer.Models)
}

func TestCodexConfig_DuplicateAccountNames(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts:
        - name: personal
        - name: work
        - name: personal
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicates account[0]`)
	assert.Contains(t, err.Error(), `"personal"`)
}

func TestCodexConfig_DuplicateAccountNamesAdjacent(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
    codex:
      accounts:
        - name: work
        - name: work
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicates account[0]`)
	assert.Contains(t, err.Error(), `"work"`)
}

func TestCodexConfig_MissingCodexBlock(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    models:
      - codex-mini
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex config is required")
}

func TestCodexConfig_ExplicitProxyOverrides(t *testing.T) {
	content := `
peers:
  my-codex:
    type: codex
    proxy: https://custom.example.com
    models:
      - codex-mini
    codex:
      accounts:
        - name: personal
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	peer := config.Peers["my-codex"]
	assert.Equal(t, "https://custom.example.com", peer.Proxy)
	assert.Equal(t, "custom.example.com", peer.ProxyURL.Host)
}
