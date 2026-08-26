package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

// readSecret reads one line from stdin without echoing it to the terminal.
//
// The standard library has no portable way to turn off terminal echo, and this CLI is
// deliberately dependency-free, so the terminal is driven through stty — which is
// specified by POSIX and present on both platforms this ships for. If stty is missing
// or refuses, the read still happens but the caller is told the key was visible, rather
// than the prompt failing outright.
//
// When stdin is not a terminal the prompt is skipped entirely and the line is read
// straight through, so `echo $KEY | council setup` works in scripts and CI.
func readSecret(w io.Writer, prompt string) (string, error) {
	if !isCharDevice(os.Stdin) {
		return readLine(os.Stdin)
	}

	// Echo is disabled before the prompt is written, never after. Turning it off costs
	// two forks of stty, and anything arriving in that window — pasted input, type-ahead,
	// or an automated driver answering the prompt — is echoed by the terminal before the
	// program can stop it. Confirmed empirically: prompting first leaks the key, and this
	// order does not.
	restore, err := disableEcho()
	if err != nil {
		fmt.Fprintf(w, "council: could not hide input (%v); it will be visible as you type\n", err)
		fmt.Fprint(w, prompt)
	} else {
		fmt.Fprint(w, prompt)
		// Echo is a property of the terminal, not the process: a ^C here would leave
		// the user's shell unable to display anything they type, long after council
		// has exited. Restore on signal as well as on return.
		stopSignals := restoreOnInterrupt(restore)
		defer stopSignals()
		defer restore()
	}

	line, readErr := readLine(os.Stdin)
	fmt.Fprintln(w) // the user's Enter was swallowed along with the echo
	return line, readErr
}

// confirm asks a yes/no question, defaulting to no. A non-terminal stdin answers no,
// so an unattended run never silently overwrites anything.
func confirm(w io.Writer, question string) bool {
	if !isCharDevice(os.Stdin) {
		return false
	}
	fmt.Fprintf(w, "%s [y/N]: ", question)
	answer, err := readLine(os.Stdin)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func readLine(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	// A pasted key is short; the default 64KiB buffer is ample.
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF // stdin closed without a line
	}
	return strings.TrimSpace(sc.Text()), nil
}

// isCharDevice reports whether f is attached to a terminal rather than a pipe or file.
func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// disableEcho turns off terminal echo and returns a function restoring the previous
// terminal state exactly, rather than assuming echo was on to begin with.
func disableEcho() (restore func(), err error) {
	saved, err := stty("-g")
	if err != nil {
		return nil, err
	}
	if _, err := stty("-echo"); err != nil {
		return nil, err
	}
	return func() { _, _ = stty(saved) }, nil
}

func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin // stty acts on the terminal behind this descriptor
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// restoreOnInterrupt runs restore if the process is interrupted, then exits. The
// returned function unregisters the handler.
func restoreOnInterrupt(restore func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			restore()
			fmt.Fprintln(os.Stderr)
			os.Exit(exitInterrupted)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
