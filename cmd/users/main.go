package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
)

func main() {
	if code := run(os.Args[1:], os.Stdin, os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	command, args := args[0], args[1:]
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		printCommandHelp(stdout, command)
		return 0
	}
	var err error
	switch command {
	case "create":
		err = createUser(args, stdin, stdout)
	case "list":
		err = listUsers(args, stdout)
	case "show":
		err = showUser(args, stdout)
	case "update":
		err = updateUser(args, stdin, stdout)
	case "delete":
		err = deleteUser(args, stdout)
	case "help", "--help", "-h":
		printHome(stdout)
		return 0
	default:
		writeError(stdout, fmt.Sprintf("unknown command %q", command), "Run `go run ./cmd/users --help` to list commands")
		return 2
	}
	if err == nil {
		return 0
	}
	var usage usageError
	if errors.As(err, &usage) {
		writeError(stdout, usage.Error(), usage.help)
		return 2
	}
	writeError(stdout, publicError(err), "Run `go run ./cmd/users --help` for command examples")
	return 1
}

type usageError struct {
	message string
	help    string
}

func (e usageError) Error() string { return e.message }

func createUser(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := newFlagSet("create")
	database := databaseFlag(fs)
	username := fs.String("username", "", "unique login username")
	displayName := fs.String("display-name", "", "account display name")
	role := fs.String("role", string(auth.RoleUser), "user, client, or admin")
	hasPurchases := fs.Bool("has-purchases", false, "status-model purchase marker")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from standard input")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error(), "Run `go run ./cmd/users create --help` for flags"}
	}
	if fs.NArg() != 0 {
		return usageError{"create does not accept positional arguments", "Run `go run ./cmd/users create --help` for flags"}
	}
	if *username == "" || *displayName == "" || !*passwordStdin {
		return usageError{"--username, --display-name, and --password-stdin are required", "Pipe a password: `printf '%s\\n' \"$PASSWORD\" | just users create <username> \"<name>\" user false`"}
	}
	password, err := readPassword(stdin)
	if err != nil {
		return err
	}
	defer clear(password)

	store, closeStore, err := openStore(*database)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	user, err := store.CreateUser(ctx, auth.CreateUserParams{Username: *username, DisplayName: *displayName, Role: auth.Role(*role), HasPurchases: *hasPurchases, Password: password})
	if err != nil {
		return err
	}
	writeUser(stdout, "created", user)
	return nil
}

func listUsers(args []string, stdout io.Writer) error {
	fs := newFlagSet("list")
	database := databaseFlag(fs)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error(), "Run `go run ./cmd/users list --help` for flags"}
	}
	if fs.NArg() != 0 {
		return usageError{"list does not accept positional arguments", "Run `go run ./cmd/users list --help` for flags"}
	}
	store, closeStore, err := openStore(*database)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	users, err := store.ListUsers(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "count: %d\n", len(users))
	if len(users) == 0 {
		fmt.Fprintln(stdout, "users: 0 users found")
		fmt.Fprintln(stdout, "help[1]: \"Pipe a password to `just users create <username> \\\"<name>\\\" user false` to create a user\"")
		return nil
	}
	fmt.Fprintf(stdout, "users[%d]{id,username,role,status}:\n", len(users))
	for _, user := range users {
		fmt.Fprintf(stdout, "  %d,%s,%s,%s\n", user.ID, toonString(user.Username), toonString(string(user.Role)), toonString(string(user.CustomerStatus())))
	}
	fmt.Fprintln(stdout, "help[1]: \"Run `just users show <username>` for account details\"")
	return nil
}

func showUser(args []string, stdout io.Writer) error {
	fs := newFlagSet("show")
	database := databaseFlag(fs)
	username := fs.String("username", "", "login username")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error(), "Run `go run ./cmd/users show --help` for flags"}
	}
	if *username == "" || fs.NArg() != 0 {
		return usageError{"--username is required and positional arguments are not accepted", "Run `just users show <username>`"}
	}
	store, closeStore, err := openStore(*database)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	user, err := store.UserByUsername(ctx, *username)
	if err != nil {
		return err
	}
	writeUser(stdout, "user", user)
	return nil
}

func updateUser(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := newFlagSet("update")
	database := databaseFlag(fs)
	username := fs.String("username", "", "current login username")
	newUsername := fs.String("new-username", "", "replacement login username")
	displayName := fs.String("display-name", "", "replacement display name")
	role := fs.String("role", "", "replacement role")
	hasPurchases := fs.Bool("has-purchases", false, "set the status-model purchase marker")
	passwordStdin := fs.Bool("password-stdin", false, "replace the password from standard input")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error(), "Run `go run ./cmd/users update --help` for flags"}
	}
	if *username == "" || fs.NArg() != 0 {
		return usageError{"--username is required and positional arguments are not accepted", "Run `go run ./cmd/users update --help` for flags"}
	}
	set := visitedFlags(fs)
	if !set["new-username"] && !set["display-name"] && !set["role"] && !set["has-purchases"] && !set["password-stdin"] {
		return usageError{"at least one update flag is required", "Set --new-username, --display-name, --role, --has-purchases, or --password-stdin"}
	}

	store, closeStore, err := openStore(*database)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current, err := store.UserByUsername(ctx, *username)
	if err != nil {
		return err
	}
	params := auth.UpdateUserParams{Username: current.Username, DisplayName: current.DisplayName, Role: current.Role, HasPurchases: current.HasPurchases, ExpectedVersion: current.Version}
	if set["new-username"] {
		params.Username = *newUsername
	}
	if set["display-name"] {
		params.DisplayName = *displayName
	}
	if set["role"] {
		params.Role = auth.Role(*role)
	}
	if set["has-purchases"] {
		params.HasPurchases = *hasPurchases
	}
	if *passwordStdin {
		params.Password, err = readPassword(stdin)
		if err != nil {
			return err
		}
		defer clear(params.Password)
	}
	updated, err := store.UpdateUser(ctx, current.ID, params)
	if err != nil {
		return err
	}
	writeUser(stdout, "updated", updated)
	return nil
}

func deleteUser(args []string, stdout io.Writer) error {
	fs := newFlagSet("delete")
	database := databaseFlag(fs)
	username := fs.String("username", "", "login username")
	confirm := fs.String("confirm", "", "repeat the username to authorize deletion")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error(), "Run `go run ./cmd/users delete --help` for flags"}
	}
	if *username == "" || *confirm != *username || fs.NArg() != 0 {
		return usageError{"--username and matching --confirm are required", "Run `just users delete <username>`"}
	}
	store, closeStore, err := openStore(*database)
	if err != nil {
		return err
	}
	defer closeStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	user, err := store.UserByUsername(ctx, *username)
	if err != nil {
		return err
	}
	if err := store.DeleteUser(ctx, user.ID); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "deleted:")
	fmt.Fprintf(stdout, "  id: %d\n", user.ID)
	fmt.Fprintf(stdout, "  username: %s\n", toonString(user.Username))
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func databaseFlag(fs *flag.FlagSet) *string {
	fallback := strings.TrimSpace(os.Getenv("DATABASE_PATH"))
	if fallback == "" {
		fallback = "data/oregon-dev-foundry.db"
	}
	return fs.String("database", fallback, "SQLite database path")
}

func openStore(database string) (*auth.Store, func(), error) {
	store, err := auth.Open(database)
	if err != nil {
		return nil, func() {}, err
	}
	return store, func() { _ = store.Close() }, nil
}

func readPassword(reader io.Reader) ([]byte, error) {
	password, err := io.ReadAll(io.LimitReader(reader, 74))
	if err != nil {
		return nil, errors.New("could not read password from standard input")
	}
	password = []byte(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	if len(password) > 72 {
		clear(password)
		return nil, errors.New("password must be at most 72 bytes")
	}
	return password, nil
}

func visitedFlags(fs *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	fs.Visit(func(flag *flag.Flag) { set[flag.Name] = true })
	return set
}

func writeUser(writer io.Writer, label string, user auth.User) {
	fmt.Fprintf(writer, "%s:\n", label)
	fmt.Fprintf(writer, "  id: %d\n", user.ID)
	fmt.Fprintf(writer, "  username: %s\n", toonString(user.Username))
	fmt.Fprintf(writer, "  displayName: %s\n", toonString(user.DisplayName))
	fmt.Fprintf(writer, "  role: %s\n", toonString(string(user.Role)))
	fmt.Fprintf(writer, "  status: %s\n", toonString(string(user.CustomerStatus())))
	fmt.Fprintf(writer, "  hasPurchases: %t\n", user.HasPurchases)
	fmt.Fprintf(writer, "  createdAt: %s\n", toonString(user.CreatedAt.Format(time.RFC3339)))
	fmt.Fprintf(writer, "  updatedAt: %s\n", toonString(user.UpdatedAt.Format(time.RFC3339)))
}

func writeError(writer io.Writer, message, help string) {
	fmt.Fprintf(writer, "error: %s\n", toonString(message))
	fmt.Fprintf(writer, "help: %s\n", toonString(help))
}

func publicError(err error) string {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return "user not found"
	case errors.Is(err, auth.ErrUsernameExists):
		return "username already exists"
	case errors.Is(err, auth.ErrConcurrentUpdate):
		return "user changed during update; reload it and retry"
	default:
		return err.Error()
	}
}

func toonString(value string) string { return strconv.Quote(value) }

func printHome(writer io.Writer) {
	fmt.Fprintln(writer, "bin: \"go run ./cmd/users\"")
	fmt.Fprintln(writer, "description: \"Manage application users in the current workspace\"")
	fmt.Fprintln(writer, "commands[5]{name,purpose}:")
	fmt.Fprintln(writer, "  create,\"Create a user; requires --password-stdin\"")
	fmt.Fprintln(writer, "  list,\"List users (the default command)\"")
	fmt.Fprintln(writer, "  show,\"Show one user by username\"")
	fmt.Fprintln(writer, "  update,\"Update account fields or password\"")
	fmt.Fprintln(writer, "  delete,\"Delete a user with explicit confirmation\"")
	fmt.Fprintln(writer, "help[3]:")
	fmt.Fprintln(writer, "  \"Use `just users list|show|create|update|delete` for standard CRUD\"")
	fmt.Fprintln(writer, "  \"Passwords are accepted only from standard input, never command arguments\"")
	fmt.Fprintln(writer, "  \"Set DATABASE_PATH or pass --database <path> to select the database\"")
}

func printCommandHelp(writer io.Writer, command string) {
	fmt.Fprintf(writer, "command: %s\n", toonString(command))
	switch command {
	case "create":
		fmt.Fprintln(writer, "usage: \"go run ./cmd/users create --username <username> --display-name \\\"<name>\\\" [--role user|client|admin] [--has-purchases] --password-stdin\"")
	case "list":
		fmt.Fprintln(writer, "usage: \"go run ./cmd/users list [--database <path>]\"")
	case "show":
		fmt.Fprintln(writer, "usage: \"go run ./cmd/users show --username <username> [--database <path>]\"")
	case "update":
		fmt.Fprintln(writer, "usage: \"go run ./cmd/users update --username <username> [--new-username <username>] [--display-name \\\"<name>\\\"] [--role user|client|admin] [--has-purchases=true|false] [--password-stdin]\"")
	case "delete":
		fmt.Fprintln(writer, "usage: \"go run ./cmd/users delete --username <username> --confirm <username> [--database <path>]\"")
	default:
		printHome(writer)
		return
	}
	fmt.Fprintln(writer, "flags:")
	fmt.Fprintln(writer, "  database: \"SQLite path; defaults to DATABASE_PATH or data/oregon-dev-foundry.db\"")
	if command == "create" || command == "update" {
		fmt.Fprintln(writer, "security: \"Passwords are read only from standard input and are never accepted as command arguments\"")
	}
}
