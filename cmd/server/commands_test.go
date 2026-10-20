package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCommand_NoArgumentsStartsTheServer(t *testing.T) {
	handled, _ := runCommand(nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if handled {
		t.Fatal("`server` with no arguments must fall through to the normal server start")
	}
}

func TestRunCommand_HelpAndUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	if handled, code := runCommand([]string{"help"}, strings.NewReader(""), &out, &errOut); !handled || code != 0 {
		t.Fatalf("help: handled=%v code=%d", handled, code)
	}
	if !strings.Contains(out.String(), "server create-org") {
		t.Fatalf("help must list the commands, got %q", out.String())
	}
	out.Reset()
	if handled, code := runCommand([]string{"serve-it"}, strings.NewReader(""), &out, &errOut); !handled || code != 2 {
		t.Fatalf("unknown command: handled=%v code=%d, want handled with exit 2", handled, code)
	}
	if !strings.Contains(errOut.String(), `unknown command "serve-it"`) {
		t.Fatalf("unknown command must say so, got %q", errOut.String())
	}
}

func TestOwnerPassword(t *testing.T) {
	t.Setenv("OWNER_PASSWORD", "")
	if p, err := ownerPassword(strings.NewReader("from-stdin\nignored\n")); err != nil || p != "from-stdin" {
		t.Fatalf("stdin: got %q, %v", p, err)
	}
	if p, err := ownerPassword(strings.NewReader("no-newline-at-end")); err != nil || p != "no-newline-at-end" {
		t.Fatalf("stdin without newline: got %q, %v", p, err)
	}
	if _, err := ownerPassword(strings.NewReader("")); err == nil {
		t.Fatal("no password anywhere must be an error")
	}
	t.Setenv("OWNER_PASSWORD", "from-env")
	if p, err := ownerPassword(strings.NewReader("from-stdin\n")); err != nil || p != "from-env" {
		t.Fatalf("OWNER_PASSWORD must win over stdin: got %q, %v", p, err)
	}
}

func TestCreateOrgRefusesStrayArguments(t *testing.T) {
	t.Setenv("OWNER_PASSWORD", "long-enough-pw")
	err := runCreateOrg(t.Context(), "postgres://unused", []string{"--name", "Acme", "--owner-email", "a@b.c", "oops"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "oops"`) {
		t.Fatalf("stray argument must be refused before touching the database, got %v", err)
	}
}
