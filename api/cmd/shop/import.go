package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// importCmd uploads receipt images through the HTTP API, so it works from
// any machine without touching the database directly.
func importCmd(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	url := flags.String("url", envOr("SHOP_URL", "http://localhost:8080"), "server URL (env SHOP_URL)")
	token := flags.String("token", os.Getenv("SHOP_TOKEN"), "API token (env SHOP_TOKEN)")
	shop := flags.Int64("shop", 0, "shop id for all images (required)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *shop == 0 || *token == "" || flags.NArg() == 0 {
		return errors.New("usage: shop import -shop ID [-url U] [-token T] <dir|file>...")
	}

	var files []string
	for _, root := range flags.Args() {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			ext := strings.ToLower(filepath.Ext(p))
			if !d.IsDir() && slices.Contains([]string{".jpg", ".jpeg", ".png"}, ext) {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	slices.Sort(files)

	client := &http.Client{Timeout: 60 * time.Second}
	var created, dup, failed int
	for _, f := range files {
		id, status, err := upload(ctx, client, *url, *token, *shop, f)
		switch {
		case err != nil:
			failed++
			fmt.Printf("FAIL  %s: %v\n", f, err)
		case status == http.StatusConflict:
			dup++
			fmt.Printf("DUP   %s (receipt %s)\n", f, id)
		default:
			created++
			fmt.Printf("OK    %s → receipt %s\n", f, id)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	fmt.Printf("\n%d uploaded, %d duplicates, %d failed\n", created, dup, failed)
	if failed > 0 {
		return fmt.Errorf("%d uploads failed", failed)
	}
	return nil
}

func upload(ctx context.Context, c *http.Client, base, token string, shop int64, path string) (string, int, error) {
	img, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("shop_id", fmt.Sprint(shop))
	fw, err := mw.CreateFormFile("image", filepath.Base(path))
	if err != nil {
		return "", 0, err
	}
	if _, err := fw.Write(img); err != nil {
		return "", 0, err
	}
	if err := mw.Close(); err != nil {
		return "", 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/receipts", &body)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out struct {
		ID     int64  `json:"id"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &out)
	switch resp.StatusCode {
	case http.StatusAccepted:
		return fmt.Sprint(out.ID), resp.StatusCode, nil
	case http.StatusConflict:
		return out.Detail, resp.StatusCode, nil
	default:
		return "", resp.StatusCode, fmt.Errorf("%s: %s %s", resp.Status, out.Title, out.Detail)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
