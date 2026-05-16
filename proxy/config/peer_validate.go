package config

import (
	"fmt"
	"time"
)

func validatePeerConfigFields(maxConcurrent, queueSize int, queueTimeout, requestInterval, timeout time.Duration) error {
	if maxConcurrent < 0 {
		return fmt.Errorf("maxConcurrent must be >= 0, got %d", maxConcurrent)
	}
	if queueSize < 0 {
		return fmt.Errorf("queueSize must be >= 0, got %d", queueSize)
	}
	if queueTimeout < 0 {
		return fmt.Errorf("queueTimeout must be >= 0, got %s", queueTimeout)
	}
	if requestInterval < 0 {
		return fmt.Errorf("requestInterval must be >= 0, got %s", requestInterval)
	}
	if timeout < 0 {
		return fmt.Errorf("timeout must be >= 0, got %s", timeout)
	}
	return nil
}
