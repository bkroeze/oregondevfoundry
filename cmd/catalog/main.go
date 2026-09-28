package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func main() {
	if code := run(os.Args[1:], os.Stdin, os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		printHome(stdout)
		return 2
	}
	command, args := args[0], args[1:]
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		printCommandHelp(stdout, command)
		return 0
	}
	if command != "fixture" && command != "apply" {
		writeError(stdout, fmt.Sprintf("unknown command %q", command), "Run `go run ./cmd/catalog --help` to list commands")
		return 2
	}
	if err := applyFixture(args, stdin, stdout); err != nil {
		writeError(stdout, err.Error(), "Run `go run ./cmd/catalog fixture --help` for command examples")
		return 1
	}
	return 0
}

func applyFixture(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("fixture", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	database := strings.TrimSpace(os.Getenv("DATABASE_PATH"))
	if database == "" {
		database = "data/oregon-dev-foundry.db"
	}
	fs.StringVar(&database, "database", database, "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("invalid fixture flags: %w", err)
	}
	if fs.NArg() != 1 {
		return errors.New("fixture requires one JSON path (use - for standard input)")
	}

	var input io.Reader = stdin
	var file *os.File
	if fs.Arg(0) != "-" {
		var err error
		file, err = os.Open(fs.Arg(0))
		if err != nil {
			return fmt.Errorf("open fixture: %w", err)
		}
		defer file.Close()
		input = file
	}
	fixture, err := commerce.DecodeProductFixture(io.LimitReader(input, 1<<20))
	if err != nil {
		return err
	}

	users, err := auth.Open(database)
	if err != nil {
		return err
	}
	defer users.Close()
	store, err := commerce.New(users.DB())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	product, err := store.ApplyProductFixture(ctx, fixture)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "applied product %q (id %d): %d SKUs, %d shipping options\n", product.Slug, product.ID, len(product.SKUs)+len(product.Addons), len(product.ShippingOptions))
	return nil
}

func writeError(writer io.Writer, message, help string) {
	fmt.Fprintf(writer, "error: %q\nhelp: %q\n", message, help)
}

func printHome(writer io.Writer) {
	fmt.Fprintln(writer, "usage: go run ./cmd/catalog fixture [--database PATH] FIXTURE.json")
	fmt.Fprintln(writer, "description: Apply an idempotent JSON product fixture")
}

func printCommandHelp(writer io.Writer, command string) {
	if command == "fixture" || command == "apply" {
		fmt.Fprintln(writer, "usage: go run ./cmd/catalog fixture [--database PATH] FIXTURE.json")
		fmt.Fprintln(writer, "FIXTURE.json may be - to read JSON from standard input.")
		return
	}
	printHome(writer)
}
