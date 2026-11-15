//go:build !windows

package backend

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTermOnlyStopsWithoutInterruptOrKill(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.termOnly = true
	installStubRunner(t, root, stubVersion, strings.Replace(stubListener, " INT\n", " TERM\n", 1), stubConfigOK)
	h, err := b.Create(context.Background(), processSpec())
	if err != nil {
		t.Fatal(err)
	}
	waitForLog(t, string(h), "listener started", 5*time.Second)
	if err := b.Stop(context.Background(), h, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(h)); !os.IsNotExist(err) {
		t.Fatalf("job directory survived: %v", err)
	}
}

func TestTermOnlyTimeoutKeepsTheProcessAndJobDirectory(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.termOnly = true
	// The stub exits cooperatively when the test releases it, never via SIGKILL.
	installStubRunner(t, root, stubVersion, "#!/bin/sh\ntrap '' TERM INT\necho ready\nwhile [ ! -f release ]; do sleep 0.1; done\n", stubConfigOK)
	h, err := b.Create(context.Background(), processSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.WriteFile(filepath.Join(string(h), "release"), nil, 0600)
		waitFor(t.Context(), func() bool { return !processAlive(readPID(string(h))) }, 5*time.Second, 20*time.Millisecond)
		b.Remove(context.Background(), h)
	}()
	waitForLog(t, string(h), "ready", 5*time.Second)
	if err := b.Stop(context.Background(), h, 50*time.Millisecond); err == nil {
		t.Fatal("timeout reported success")
	}
	if !processAlive(readPID(string(h))) {
		t.Fatal("TERM-resistant process was killed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Remove(ctx, h); err == nil {
		t.Fatal("removed a live job")
	}
	if _, err := os.Stat(string(h)); err != nil {
		t.Fatal(err)
	}
}

func TestTermOnlyKeepsTheSlotUntilTheWorkersExit(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.termOnly = true
	installStubRunner(t, root, stubVersion, "#!/bin/sh\ntrap 'exit 0' TERM\nsh -c 'trap \"\" TERM INT; echo child-ready; while [ ! -f release ]; do sleep 0.1; done' &\nwait\n", stubConfigOK)
	h, err := b.Create(context.Background(), processSpec())
	if err != nil {
		t.Fatal(err)
	}
	pid := readPID(string(h))
	defer func() {
		os.WriteFile(filepath.Join(string(h), "release"), nil, 0600)
		waitFor(context.Background(), func() bool { return !runnerGroupAlive(pid) }, 5*time.Second, 20*time.Millisecond)
		b.Remove(context.Background(), h)
	}()
	waitForLog(t, string(h), "child-ready", 5*time.Second)
	if err := b.Stop(context.Background(), h, 100*time.Millisecond); err == nil {
		t.Fatal("reported success while a worker survived")
	}
	if err := b.Remove(context.Background(), h); err == nil {
		t.Fatal("deleted a job with a live worker")
	}
	if _, err := os.Stat(string(h)); err != nil {
		t.Fatal(err)
	}
}

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
