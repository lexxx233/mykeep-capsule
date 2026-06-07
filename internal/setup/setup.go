// Package setup handles the password-before-serving launch flow (PLAN §11.1):
// first launch creates the decryption password; every launch prompts for it.
package setup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrNoPassphrase means no TTY and no JOYVEND_PASSPHRASE/stdin was available.
var ErrNoPassphrase = errors.New("joyvend: no passphrase provided (set JOYVEND_PASSPHRASE or run on a terminal)")

// ReadPassphrase prompts once for the decryption password. Order: JOYVEND_PASSPHRASE
// env (then unset), piped stdin, interactive TTY. Refuses (ErrNoPassphrase) if none.
func ReadPassphrase(prompt string) ([]byte, error) {
	if env := os.Getenv("JOYVEND_PASSPHRASE"); env != "" {
		os.Unsetenv("JOYVEND_PASSPHRASE")
		return []byte(env), nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		p, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return p, err
	}
	// piped stdin (first non-empty line)
	line, err := readLine(os.Stdin)
	if err != nil || len(line) == 0 {
		return nil, ErrNoPassphrase
	}
	return line, nil
}

// ReadNewPassphrase prompts (twice, to catch typos) when creating the password at
// first launch. There is NO complexity policy — the user owns their passphrase
// strength — but there is also NO recovery, which we make loud and clear.
func ReadNewPassphrase() ([]byte, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return ReadPassphrase("") // headless: accept whatever's provided
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "⚠  This password encrypts ALL your memories. There is NO recovery —")
	fmt.Fprintln(os.Stderr, "   if you forget it, your memories are gone forever. Choose accordingly.")
	for {
		fmt.Fprint(os.Stderr, "Create a decryption password: ")
		p1, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, err
		}
		if len(p1) == 0 {
			fmt.Fprintln(os.Stderr, "  password cannot be empty.")
			continue
		}
		fmt.Fprint(os.Stderr, "Confirm password: ")
		p2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(p1, p2) {
			fmt.Fprintln(os.Stderr, "  passwords did not match — try again.")
			continue
		}
		return p1, nil
	}
}

func readLine(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	return []byte(strings.TrimRight(line, "\r\n")), err
}
