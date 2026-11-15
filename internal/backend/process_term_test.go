//go:build !windows

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestMacDefaultStopsWithTERM(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS stop signal")
	}
	b, root := newStubProcessBackend(t)
	installStubRunner(t, root, stubVersion, "#!/bin/sh\ntrap 'echo terminated; exit 0' TERM\ntrap '' INT\necho ready\nwhile :; do sleep 0.1; done\n", stubConfigOK)
	h, err := b.Create(context.Background(), processSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Remove(context.Background(), h)
	waitForLog(t, string(h), "ready", 5*time.Second)
	if err := b.Stop(context.Background(), h, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, string(h), "terminated", time.Second)
}

func TestMacDefaultWaitsForWorkersAndEscalatesAfterTERM(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS process groups")
	}
	b, root := newStubProcessBackend(t)
	installStubRunner(t, root, stubVersion, "#!/bin/sh\ntrap 'echo terminated; exit 0' TERM\nsh -c 'trap \"\" TERM INT; echo child-ready; while :; do sleep 0.1; done' &\nwait\n", stubConfigOK)
	h, err := b.Create(context.Background(), processSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Remove(context.Background(), h)
	waitForLog(t, string(h), "child-ready", 5*time.Second)
	pid := readPID(string(h))
	started := time.Now()
	if err := b.Stop(context.Background(), h, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 100*time.Millisecond {
		t.Fatal("did not allow TERM its timeout")
	}
	waitForLog(t, string(h), "terminated", time.Second)
	if runnerGroupAlive(pid) {
		t.Fatal("worker group survived escalation")
	}
	if err := b.Remove(context.Background(), h); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFailedStartStopsAResistantChildWithinTheTimeout(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS startup cleanup")
	}
	b, root := newStubProcessBackend(t)
	log, err := os.Create(filepath.Join(root, "runner.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; echo ready; while :; do sleep 0.1; done")
	cmd.Stdout, cmd.Stderr = log, log
	detachRunner(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer signalRunner(cmd.Process, syscall.SIGKILL)
	waitForLog(t, root, "ready", 5*time.Second)
	if err := b.abandon(cmd, log, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if runnerGroupAlive(cmd.Process.Pid) {
		t.Fatal("failed startup left a live child")
	}
}
