package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx", for goose

	"openloc/internal/auth"
	"openloc/internal/config"
	"openloc/internal/database"
)

const commandsUsage = `Usage:
  server                    start the server
  server migrate            apply pending database migrations, then exit
  server create-org --name <org> --owner-email <email>
                            create an organization and its owner, then exit.
                            The owner's password comes from OWNER_PASSWORD,
                            or from the first line of stdin.
`

// runCommand runs `server <command>` and reports whether args named one.
// With no arguments it returns false, and main starts the server.
func runCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}
	ctx := context.Background()
	var err error
	switch args[0] {
	case "migrate":
		err = runMigrate(ctx, config.Load().DatabaseURL, stdout)
	case "create-org":
		err = runCreateOrg(ctx, config.Load().DatabaseURL, args[1:], stdin, stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, commandsUsage)
		return true, 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], commandsUsage)
		return true, 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", args[0], err)
		return true, 1
	}
	return true, 0
}

func runMigrate(ctx context.Context, databaseURL string, stdout io.Writer) error {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqlDB.Close()
	applied, err := database.Migrate(ctx, sqlDB)
	if err != nil {
		return err
	}

	pool, err := database.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	if err := database.VerifySchema(ctx, pool); err != nil {
		return fmt.Errorf("schema check after migrating: %w", err)
	}
	fmt.Fprintf(stdout, "migrations applied: %d; schema is up to date\n", applied)
	return nil
}

func runCreateOrg(ctx context.Context, databaseURL string, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("create-org", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "organization name")
	email := fs.String("owner-email", "", "owner's email; they sign in with it")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n\n%s", err, commandsUsage)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q\n\n%s", fs.Arg(0), commandsUsage)
	}

	password, err := ownerPassword(stdin)
	if err != nil {
		return err
	}

	pool, err := database.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	if err := database.VerifySchema(ctx, pool); err != nil {
		return fmt.Errorf("the database is not migrated (run `server migrate` first): %w", err)
	}

	tenant, user, err := auth.CreateOrgWithOwner(ctx, pool, *name, *email, password)
	if errors.Is(err, auth.ErrAccountExists) {
		return fmt.Errorf("%w; sign in with it, or choose another owner email", err)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created organization %q (id %d) with owner %s\n", tenant.Name, tenant.ID, user.Email)
	return nil
}

// ownerPassword reads OWNER_PASSWORD, else the first line of stdin. It never
// comes from the command line, where it would land in shell history.
func ownerPassword(stdin io.Reader) (string, error) {
	if p := os.Getenv("OWNER_PASSWORD"); p != "" {
		return p, nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	p := strings.TrimRight(line, "\r\n")
	if p == "" {
		return "", errors.New("no password: set OWNER_PASSWORD, or pass the password on stdin")
	}
	return p, nil
}
