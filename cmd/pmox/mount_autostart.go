package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// The login job that brings mounts back: `pmox remount --stale` at login
// and every few minutes, so a mount whose process ended (a reboot, the VM
// or Proxmox unreachable when it started) comes back on its own.
const (
	autostartLabel    = "com.github.eugenetaranov.pmox.mounts"
	autostartInterval = 300 // seconds
)

// autostartRunFn runs launchctl or systemctl. A seam for tests.
var autostartRunFn = func(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return nil
}

// autostartGOOS is runtime.GOOS (tests change it).
var autostartGOOS = runtime.GOOS

func newMountAutostartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "autostart on|off",
		Short: "Bring background mounts back at login and when they stop",
		Long: `'on' installs a login job (a launchd agent on macOS, a systemd user
timer on Linux) that runs 'pmox remount --stale' at login and every five
minutes: a mount whose process ended (a reboot, a logout, the VM or
Proxmox unreachable for a while) is started again with its options.
Mounts stopped with 'pmox umount' stay stopped. 'off' removes the job.`,
		Example:   "  pmox mount autostart on",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// From the command palette: ask on a terminal.
				if !tui.Interactive() {
					return fmt.Errorf("%w: usage: pmox mount autostart on|off", exitcode.ErrUserInput)
				}
				v, err := tui.Select("Bring background mounts back at login?", []huh.Option[string]{
					huh.NewOption("on: restart stopped mounts at login and every 5 minutes", "on"),
					huh.NewOption("off: remove the login job", "off"),
				})
				if err != nil {
					return err
				}
				args = []string{v}
			}
			switch args[0] {
			case "on":
				return autostartOn(cmd)
			case "off":
				return autostartOff(cmd)
			}
			return fmt.Errorf("%w: want on or off", exitcode.ErrUserInput)
		},
	}
}

// pmoxPath is the pmox to run from the job: the one on PATH (Homebrew's
// /opt/homebrew/bin/pmox survives upgrades; the Cellar path doesn't).
func pmoxPath() (string, error) {
	if p, err := exec.LookPath("pmox"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
	}
	return os.Executable()
}

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", autostartLabel+".plist"), nil
}

func systemdUserDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func launchAgent(exe string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + autostartLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + exe + `</string>
		<string>remount</string>
		<string>--stale</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(autostartInterval) + `</integer>
	<!-- The mounts it starts are daemons of their own: launchd mustn't
	     stop them when this job ends. -->
	<key>AbandonProcessGroup</key>
	<true/>
</dict>
</plist>
`
}

func systemdUnits(exe string) (service, timer string) {
	service = `[Unit]
Description=Bring pmox's background mounts back (pmox mount autostart)

[Service]
Type=oneshot
ExecStart=` + exe + ` remount --stale
KillMode=process
`
	timer = `[Unit]
Description=Bring pmox's background mounts back, at login and every 5 minutes

[Timer]
OnStartupSec=30
OnUnitActiveSec=` + strconv.Itoa(autostartInterval) + `
Unit=pmox-mounts.service

[Install]
WantedBy=timers.target
`
	return service, timer
}

func autostartOn(cmd *cobra.Command) error {
	exe, err := pmoxPath()
	if err != nil {
		return err
	}
	out := cmd.ErrOrStderr()
	switch autostartGOOS {
	case "darwin":
		p, err := launchAgentPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(launchAgent(exe)), 0o644); err != nil {
			return err
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = autostartRunFn("launchctl", "bootout", domain+"/"+autostartLabel) // a previous one
		if err := autostartRunFn("launchctl", "bootstrap", domain, p); err != nil {
			return err
		}
		fmt.Fprintf(out, "%sautostart on: %s runs 'pmox remount --stale' at login and every 5 minutes\n", doneMark(true), p)
	case "linux":
		dir, err := systemdUserDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		service, timer := systemdUnits(exe)
		if err := os.WriteFile(filepath.Join(dir, "pmox-mounts.service"), []byte(service), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "pmox-mounts.timer"), []byte(timer), 0o644); err != nil {
			return err
		}
		if err := autostartRunFn("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := autostartRunFn("systemctl", "--user", "enable", "--now", "pmox-mounts.timer"); err != nil {
			return err
		}
		fmt.Fprintf(out, "%sautostart on: pmox-mounts.timer runs 'pmox remount --stale' at login and every 5 minutes\n", doneMark(true))
	default:
		return fmt.Errorf("autostart is for macOS and Linux, not %s", autostartGOOS)
	}
	return nil
}

func autostartOff(cmd *cobra.Command) error {
	out := cmd.ErrOrStderr()
	switch autostartGOOS {
	case "darwin":
		p, err := launchAgentPath()
		if err != nil {
			return err
		}
		_ = autostartRunFn("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+autostartLabel)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	case "linux":
		_ = autostartRunFn("systemctl", "--user", "disable", "--now", "pmox-mounts.timer")
		dir, err := systemdUserDir()
		if err != nil {
			return err
		}
		for _, f := range []string{"pmox-mounts.timer", "pmox-mounts.service"} {
			if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		_ = autostartRunFn("systemctl", "--user", "daemon-reload")
	default:
		return fmt.Errorf("autostart is for macOS and Linux, not %s", autostartGOOS)
	}
	fmt.Fprintf(out, "%sautostart off (running mounts keep running)\n", doneMark(true))
	return nil
}
