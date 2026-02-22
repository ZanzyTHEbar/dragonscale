package commands

import (
	"bytes"
	"testing"
)

func TestParseCronOptions(t *testing.T) {
	t.Parallel()

	t.Run("builds options from CLI args", func(t *testing.T) {
		opts, err := parseCronOptions("nightly", "250", "0 0 * * *")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Name != "nightly" {
			t.Fatalf("expected name nightly, got %q", opts.Name)
		}
		if opts.EveryMS == nil || *opts.EveryMS != 250 {
			t.Fatalf("expected everyMS=250, got %#v", opts.EveryMS)
		}
		if opts.Cron != "0 0 * * *" {
			t.Fatalf("expected cron expr, got %q", opts.Cron)
		}
	})

	t.Run("returns error for invalid every value", func(t *testing.T) {
		if _, err := parseCronOptions("bad", "not-a-number", ""); err == nil {
			t.Fatal("expected parse error for invalid every value")
		}
	})

	t.Run("returns default options for empty inputs", func(t *testing.T) {
		opts, err := parseCronOptions("", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Name != "" || opts.Cron != "" || opts.EveryMS != nil {
			t.Fatalf("expected empty defaults, got %#v", opts)
		}
	})
}

func TestCronListCommandRequiresService(t *testing.T) {
	cmd := buildCronListCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true

	err := cmd.Execute()
	if err == nil || err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error for cron list: %v", err)
	}
}

func TestCronAddCommandRequiresService(t *testing.T) {
	cmd := buildCronAddCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true

	// verify high-risk flag surface is wired before execution
	for _, name := range []string{"name", "message", "every", "cron", "deliver", "to", "channel"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing cron flag: %s", name)
		}
	}

	err := cmd.Execute()
	if err == nil || err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error for cron add: %v", err)
	}
}

func TestSkillsListCommandRequiresService(t *testing.T) {
	cmd := buildSkillsListCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true

	err := cmd.Execute()
	if err == nil || err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error for skills list: %v", err)
	}
}

func TestStatusCommandRequiresService(t *testing.T) {
	cmd := buildStatusCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true

	err := cmd.Execute()
	if err == nil || err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error for status: %v", err)
	}
}

func TestDaemonStatusCommandRequiresService(t *testing.T) {
	cmd := buildDaemonStatusCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true

	err := cmd.Execute()
	if err == nil || err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error for daemon status: %v", err)
	}
}

func TestGatewayCommandHasDebugFlag(t *testing.T) {
	cmd := buildGatewayCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if cmd.Flags().Lookup("debug") == nil {
		t.Fatalf("missing gateway debug flag")
	}
}

func TestGatewayCommandRequiresService(t *testing.T) {
	cmd := buildGatewayCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true

	err := cmd.Execute()
	if err == nil || err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error for gateway: %v", err)
	}
}
