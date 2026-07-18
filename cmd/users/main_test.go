package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUserCRUDCommand(t *testing.T) {
	database := t.TempDir() + "/users.db"
	password := "correct horse battery staple"
	var output bytes.Buffer

	code := run([]string{"create", "--database", database, "--username", "client.one", "--display-name", "Client One", "--role", "client", "--password-stdin"}, strings.NewReader(password+"\n"), &output)
	if code != 0 || !strings.Contains(output.String(), "status: \"Client\"") {
		t.Fatalf("create code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"list", "--database", database}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "count: 1") || !strings.Contains(output.String(), "client.one") {
		t.Fatalf("list code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"show", "--database", database, "--username", "client.one"}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "displayName: \"Client One\"") {
		t.Fatalf("show code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"update", "--database", database, "--username", "client.one", "--display-name", "Customer One", "--role", "user", "--has-purchases=true"}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "status: \"Customer\"") {
		t.Fatalf("update code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"delete", "--database", database, "--username", "client.one", "--confirm", "client.one"}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "deleted:") {
		t.Fatalf("delete code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"list", "--database", database}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "users: 0 users found") {
		t.Fatalf("empty list code=%d output=%s", code, output.String())
	}
}

func TestUserCommandRequiresPasswordFromStandardInput(t *testing.T) {
	var output bytes.Buffer
	code := run([]string{"create", "--database", t.TempDir() + "/users.db", "--username", "safe.user", "--display-name", "Safe User", "--password", "secret-on-command-line"}, strings.NewReader(""), &output)
	if code != 2 || !strings.Contains(output.String(), "flag provided but not defined") {
		t.Fatalf("code=%d output=%s", code, output.String())
	}
}

func TestSubcommandHelpSucceeds(t *testing.T) {
	var output bytes.Buffer
	if code := run([]string{"create", "--help"}, strings.NewReader(""), &output); code != 0 || !strings.Contains(output.String(), "--password-stdin") {
		t.Fatalf("code=%d output=%s", code, output.String())
	}
}
