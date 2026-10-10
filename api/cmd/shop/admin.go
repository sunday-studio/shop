package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"shop/internal/auth"
	"shop/internal/store/db"
)

func quietApp(ctx context.Context) (*app, error) {
	return openApp(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func tokenCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: shop token create|list|revoke [name]")
	}
	a, err := quietApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()
	q := db.New(a.db)

	switch args[0] {
	case "create":
		if len(args) != 2 {
			return errors.New("usage: shop token create <name>")
		}
		tok, err := auth.Create(ctx, q, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "token %q created; it is shown only once:\n", args[1])
		fmt.Println(tok)
	case "list":
		toks, err := q.ListTokens(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tCREATED\tLAST USED")
		for _, t := range toks {
			last := "never"
			if t.LastUsedAt.Valid {
				last = t.LastUsedAt.String
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.CreatedAt, last)
		}
		return tw.Flush()
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: shop token revoke <name>")
		}
		n, err := q.DeleteToken(ctx, args[1])
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no token named %q", args[1])
		}
		fmt.Println("revoked", args[1])
	default:
		return fmt.Errorf("unknown token subcommand %q", args[0])
	}
	return nil
}

func reparseCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reparse", flag.ContinueOnError)
	status := fs.String("status", "", "only receipts with this status (e.g. needs_review)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := quietApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()
	n, err := a.receipts.ReparseAll(ctx, *status)
	if err != nil {
		return err
	}
	fmt.Printf("queued %d receipts for re-parse (the running server processes them)\n", n)
	return nil
}

// passwordCmd manages the pairing password the app uses to connect.
func passwordCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("password", flag.ContinueOnError)
	revoke := fs.Bool("revoke-devices", false, "also unpair every device (they must pair again)")
	if len(args) == 0 {
		return errors.New("usage: shop password generate|set|status [-revoke-devices]")
	}
	sub := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	a, err := quietApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()
	q := db.New(a.db)

	var pw string
	switch sub {
	case "status":
		ok, err := auth.HasPassword(ctx, q)
		if err != nil {
			return err
		}
		devs, err := q.ListDeviceTokens(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("pairing password set: %v\npaired devices: %d\n", ok, len(devs))
		for _, d := range devs {
			fmt.Printf("  %s (since %s)\n", d.Name, d.CreatedAt)
		}
		return nil
	case "generate":
		pw = auth.GeneratePassphrase()
	case "set":
		if pw, err = readPassword(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown password subcommand %q", sub)
	}
	if err := auth.SetPassword(ctx, q, pw); err != nil {
		return err
	}
	if *revoke {
		n, err := q.DeleteDeviceTokens(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "unpaired %d device(s)\n", n)
	}
	if sub == "generate" {
		fmt.Fprintln(os.Stderr, "pairing password (shown once; enter it in the app with the server URL):")
		fmt.Println(pw)
	} else {
		fmt.Fprintln(os.Stderr, "pairing password updated")
	}
	return nil
}

// readPassword prompts twice on a terminal, or reads one line from stdin.
func readPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "new pairing password: ")
		a, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(os.Stderr, "again: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			return "", errors.New("passwords do not match")
		}
		return string(a), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
