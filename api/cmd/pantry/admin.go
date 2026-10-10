package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"

	"pantry/internal/auth"
	"pantry/internal/store/db"
)

func quietApp(ctx context.Context) (*app, error) {
	return openApp(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func tokenCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pantry token create|list|revoke [name]")
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
			return errors.New("usage: pantry token create <name>")
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
			return errors.New("usage: pantry token revoke <name>")
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
