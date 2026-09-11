// Command seed creates (or reuses) a user and issues a fresh API key, printing
// the plaintext key exactly once. Use it to bootstrap local development.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/store"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	email := flag.String("email", "dev@bepilot.local", "user email")
	name := flag.String("name", "Local Dev", "user display name")
	keyName := flag.String("key-name", "local", "api key label")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db:", err)
		os.Exit(1)
	}
	defer st.Close()

	user, err := st.Users.Create(ctx, *email, *name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create user:", err)
		os.Exit(1)
	}
	plaintext, key, err := st.APIKeys.Issue(ctx, user.ID, *keyName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "issue key:", err)
		os.Exit(1)
	}

	fmt.Printf("user_id:  %s\n", user.ID)
	fmt.Printf("email:    %s\n", user.Email)
	fmt.Printf("key_id:   %s\n", key.ID)
	fmt.Printf("api_key:  %s\n", plaintext)
	fmt.Println("\nExport it for the curl examples:")
	fmt.Printf("  export BEPILOT_API_KEY=%s\n", plaintext)
}
