package codex

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

// RunLogin executes the Codex device auth login flow.
func RunLogin(ctx context.Context, accountName string, authPath string) error {
	store := NewAuthStore(authPath)
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load auth store: %w", err)
	}

	fmt.Printf("[CODEX] Starting device auth for account %q ...\n", accountName)

	deviceAuth, err := StartDeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("failed to start device auth: %w", err)
	}

	fmt.Printf("[CODEX] Please visit: %s\n", deviceAuth.VerificationURL())
	fmt.Printf("[CODEX] And enter code: %s\n", deviceAuth.UserCode)
	fmt.Println("[CODEX] Waiting for authorization...")

	pollCtx, pollCancel := context.WithDeadline(ctx, deviceAuth.ExpiresTime().Add(30*time.Second))
	defer pollCancel()

	token, err := PollDeviceAuth(pollCtx, deviceAuth)
	if err != nil {
		return fmt.Errorf("device auth failed: %w", err)
	}

	if err := store.SetToken(accountName, token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	fmt.Printf("[CODEX] \u2713 Account %q authenticated successfully\n", accountName)
	if token.AccountID != "" {
		fmt.Printf("[CODEX] Account ID: %s\n", token.AccountID)
	}

	return nil
}

// RunLogout removes a stored Codex account token.
func RunLogout(accountName string, authPath string) error {
	store := NewAuthStore(authPath)
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load auth store: %w", err)
	}

	if err := store.RemoveToken(accountName); err != nil {
		return err
	}

	fmt.Printf("[CODEX] \u2713 Account %q removed\n", accountName)
	return nil
}

// RunListAccounts lists all stored Codex accounts with expiry status.
func RunListAccounts(authPath string) error {
	store := NewAuthStore(authPath)
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load auth store: %w", err)
	}

	accounts := store.ListAccounts()
	if len(accounts) == 0 {
		fmt.Println("[CODEX] No accounts configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tTOKEN STATUS")
	for _, name := range accounts {
		token, err := store.GetToken(name)
		if err != nil {
			fmt.Fprintf(w, "%s\terror: %s\n", name, err)
			continue
		}
		status := "expired"
		if time.Now().Unix() < token.ExpiresAt-tokenExpiryLeeway {
			status = "valid"
		}
		fmt.Fprintf(w, "%s\t%s\n", name, status)
	}
	w.Flush()

	return nil
}
