package codex

import (
	"context"
	"sync"
	"time"
)

const (
	minRefreshRetryDelay = 30 * time.Second
	maxRefreshRetryDelay = 5 * time.Minute
)

// Refresher proactively refreshes Codex auth tokens before they expire.
// It runs a single background goroutine that wakes at the precise moment
// the earliest token needs refresh (ExpiresAt - tokenExpiryLeeway).
type Refresher struct {
	auth   *AuthStore
	logger func(format string, args ...any)

	getValidToken func(accountName string) (*TokenData, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRefresher creates a Refresher for the given AuthStore.
// Call Start() to begin the background refresh loop, and Stop() to shut it down.
func NewRefresher(auth *AuthStore, logger func(format string, args ...any)) *Refresher {
	if logger == nil {
		logger = func(format string, args ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Refresher{
		auth:          auth,
		logger:        logger,
		getValidToken: auth.GetValidToken,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Start begins the background refresh goroutine.
func (r *Refresher) Start() {
	r.wg.Add(1)
	go r.run()
}

// Stop cancels the refresh goroutine and waits for it to exit.
func (r *Refresher) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *Refresher) run() {
	defer r.wg.Done()

	retryAfter := make(map[string]time.Time)
	retryDelay := make(map[string]time.Duration)

	for {
		wait := r.computeNextRefresh(retryAfter, retryDelay)

		timer := time.NewTimer(wait)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// computeNextRefresh checks all accounts and refreshes those that are due.
// Returns the duration until the next refresh is needed.
func (r *Refresher) computeNextRefresh(retryAfter map[string]time.Time, retryDelay map[string]time.Duration) time.Duration {
	now := time.Now()
	expiries := r.auth.ListTokenExpiries()

	if len(expiries) == 0 {
		return 1 * time.Minute
	}

	nextWake := now.Add(1 * time.Hour)

	for _, expiry := range expiries {
		account := expiry.AccountName
		refreshAt := time.Unix(expiry.ExpiresAt-tokenExpiryLeeway, 0)

		// If in retry backoff, skip until retryAfter.
		if retryAt, ok := retryAfter[account]; ok && now.Before(retryAt) {
			if retryAt.Before(nextWake) {
				nextWake = retryAt
			}
			continue
		}

		// If not yet due for refresh, schedule wake at refreshAt.
		if now.Before(refreshAt) {
			if refreshAt.Before(nextWake) {
				nextWake = refreshAt
			}
			continue
		}

		// Account is due for refresh.
		_, err := r.getValidToken(account)
		if err != nil {
			delay := retryDelay[account]
			if delay == 0 {
				delay = minRefreshRetryDelay
			} else {
				delay *= 2
				if delay > maxRefreshRetryDelay {
					delay = maxRefreshRetryDelay
				}
			}
			retryDelay[account] = delay
			retryAfter[account] = now.Add(delay)

			r.logger("[CODEX REFRESH] failed for account %q, retry in %s: %v", account, delay, err)

			if retryAfter[account].Before(nextWake) {
				nextWake = retryAfter[account]
			}
			continue
		}

		// Success clears retry state.
		delete(retryAfter, account)
		delete(retryDelay, account)

		// Token was refreshed, resnapshot soon to get new expiry.
		resnapshotAt := now.Add(2 * time.Second)
		if resnapshotAt.Before(nextWake) {
			nextWake = resnapshotAt
		}
	}

	wait := time.Until(nextWake)
	if wait < time.Second {
		return time.Second
	}
	return wait
}
