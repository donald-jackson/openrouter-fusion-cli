package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestReadQuestionFromArgs(t *testing.T) {
	got, err := readQuestion([]string{"is", "an", "LLC", "fine?"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if got != "is an LLC fine?" {
		t.Errorf("readQuestion = %q", got)
	}
}

func TestReadQuestionFromStdin(t *testing.T) {
	for _, args := range [][]string{nil, {"-"}} {
		got, err := readQuestion(args, strings.NewReader("  piped question\n"))
		if err != nil {
			t.Fatalf("args %v: %v", args, err)
		}
		if got != "piped question" {
			t.Errorf("args %v: readQuestion = %q", args, got)
		}
	}
}

func TestReadQuestionEmptyIsUsageError(t *testing.T) {
	_, err := readQuestion(nil, strings.NewReader("   \n"))
	var usageErr *errUsage
	if !errors.As(err, &usageErr) {
		t.Fatalf("got %T (%v), want *errUsage", err, err)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" a , b ,, c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(splitList("")) != 0 {
		t.Error("an empty string should yield no entries")
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if code := run([]string{"summon"}); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if code := run(nil); code != exitUsage {
		t.Errorf("no args exit code = %d, want %d", code, exitUsage)
	}
}

func TestRunVersionAndHelp(t *testing.T) {
	for _, cmd := range []string{"version", "--version", "help", "--help"} {
		if code := run([]string{cmd}); code != exitOK {
			t.Errorf("%q exit code = %d, want %d", cmd, code, exitOK)
		}
	}
}

// TestAskRejectsBadFlagsBeforeSpending guards the money: argument validation must
// fail before any network call is made.
func TestAskRejectsBadFlagsBeforeSpending(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"bad format", []string{"-format", "yaml", "q"}},
		{"tool calls too high", []string{"-max-tool-calls", "99", "q"}},
		{"tool calls too low", []string{"-max-tool-calls", "0", "q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmdAsk(tc.args)
			var usageErr *errUsage
			if !errors.As(err, &usageErr) {
				t.Fatalf("got %T (%v), want *errUsage", err, err)
			}
		})
	}
}

// TestFlagsAfterQuestionAreParsed pins a bug that cost a live API call: Go's flag
// package stops at the first positional, so `ask "question" --full` used to fold
// "--full" into the question, ship it to the models, and bill for the run.
func TestFlagsAfterQuestionAreParsed(t *testing.T) {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	full := fs.Bool("full", false, "")
	format := fs.String("format", "human", "")

	positionals, err := parseInterspersed(fs, []string{"why", "is", "this", "hard?", "--full", "-format", "json"})
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(positionals, " "); got != "why is this hard?" {
		t.Errorf("question = %q; flags must not leak into it", got)
	}
	if !*full {
		t.Error("-full after the question was not parsed")
	}
	if *format != "json" {
		t.Errorf("-format after the question = %q, want json", *format)
	}
}

func TestFlagsBeforeQuestionStillWork(t *testing.T) {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	full := fs.Bool("full", false, "")

	positionals, err := parseInterspersed(fs, []string{"--full", "the", "question"})
	if err != nil {
		t.Fatal(err)
	}
	if !*full || strings.Join(positionals, " ") != "the question" {
		t.Errorf("full=%v positionals=%v", *full, positionals)
	}
}

func TestParseInterspersedReportsBadFlags(t *testing.T) {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Bool("full", false, "")

	if _, err := parseInterspersed(fs, []string{"q", "-nonexistent"}); err == nil {
		t.Error("an unknown flag after the question must still be an error")
	}
}

func TestReportExitCodes(t *testing.T) {
	if got := report(nil); got != exitOK {
		t.Errorf("report(nil) = %d", got)
	}
	if got := report(&errUsage{msg: "bad flag"}); got != exitUsage {
		t.Errorf("usage error = %d, want %d", got, exitUsage)
	}
}
