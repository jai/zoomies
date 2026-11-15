package backend

import (
	"context"
	"encoding/json"
	"github.com/eyupio/zoomies/internal/store"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestNativeSlotsReserveBeforeRegistrationAndReleaseAfterRemoval(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.termOnly = true
	installStubRunner(t, root, stubVersion, "#!/bin/sh\ntrap 'exit 0' TERM\necho ready\nwhile :; do sleep 1; done\n", stubConfigOK)
	b.admissionFile = filepath.Join(root, "admission.json")
	policy := ProcessAdmission{Directory: filepath.Join(root, "slots"), Capacity: 2, Env: map[string]string{"GATE_SLOT": "test-{slot}"}}
	data, _ := json.Marshal(policy)
	os.WriteFile(b.admissionFile, data, 0600)
	handles := make(chan Handle, 3)
	errs := make(chan error, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			spec := processSpec()
			spec.Name += "-" + strconv.Itoa(i)
			h, err := b.Create(context.Background(), spec)
			if err != nil {
				errs <- err
			} else {
				handles <- h
			}
		}(i)
	}
	wg.Wait()
	close(handles)
	close(errs)
	if len(handles) != 2 || len(errs) != 1 {
		t.Fatalf("created %d runners, errors %d", len(handles), len(errs))
	}
	for err := range errs {
		if Fault(err) != store.FaultBackendBusy {
			t.Fatal(err)
		}
	}
	seen := map[int]bool{}
	for h := range handles {
		lease := readProcessLease(string(h))
		if lease == nil || seen[lease.Slot] {
			t.Fatal("missing or duplicate slot")
		}
		seen[lease.Slot] = true
		waitForLog(t, string(h), "ready", 5*time.Second)
		if err := b.Stop(context.Background(), h, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := b.Remove(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(policy.Directory)
	if len(entries) != 0 {
		t.Fatal("slot not released")
	}
}

func TestNativeAdmissionPreservesOtherOwnersAndFailsClosed(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.admissionFile = filepath.Join(root, "policy")
	dir := filepath.Join(root, "job")
	os.Mkdir(dir, 0700)
	spec := processSpec()
	if _, err := b.reserveSlot(dir, &spec); err == nil {
		t.Fatal("missing policy admitted a job")
	}
	policy := ProcessAdmission{Directory: filepath.Join(root, "slots"), Capacity: 1}
	data, _ := json.Marshal(policy)
	os.WriteFile(b.admissionFile, data, 0600)
	occupied := filepath.Join(policy.Directory, "1")
	os.MkdirAll(occupied, 0700)
	os.WriteFile(filepath.Join(occupied, "owner"), []byte("legacy-lane"), 0600)
	if _, err := b.reserveSlot(dir, &spec); Fault(err) != store.FaultBackendBusy {
		t.Fatalf("occupied slot: %v", err)
	}
	owner, _ := os.ReadFile(filepath.Join(occupied, "owner"))
	if string(owner) != "legacy-lane" {
		t.Fatal("modified another owner")
	}
	policy.Capacity = 0
	data, _ = json.Marshal(policy)
	os.WriteFile(b.admissionFile, data, 0600)
	if _, err := b.reserveSlot(dir, &spec); Fault(err) != store.FaultBackendBusy {
		t.Fatalf("paused policy: %v", err)
	}
}

func TestNativeAdmissionExportsItsOwnSlotAndRejectsExpiredPolicy(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.admissionFile = filepath.Join(root, "policy")
	dir := filepath.Join(root, "job")
	os.Mkdir(dir, 0700)
	policy := ProcessAdmission{Directory: filepath.Join(root, "slots"), Capacity: 1, Env: map[string]string{"GATE_SLOT": "c03-{slot}", "TMPDIR": "{job}/tmp"}}
	data, _ := json.Marshal(policy)
	os.WriteFile(b.admissionFile, data, 0600)
	spec := processSpec()
	lease, err := b.reserveSlot(dir, &spec)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["CI_SLOT"] != "1" || spec.Env["GATE_SLOT"] != "c03-1" || spec.Env["TMPDIR"] != filepath.Join(dir, "tmp") {
		t.Fatalf("slot env: %v", spec.Env)
	}
	if _, err := os.Stat(spec.Env["TMPDIR"]); err != nil {
		t.Fatal(err)
	}
	lease.release()
	policy.ExpiresAt = float64(time.Now().Add(-time.Minute).Unix())
	data, _ = json.Marshal(policy)
	os.WriteFile(b.admissionFile, data, 0600)
	if _, err := b.reserveSlot(dir, &spec); Fault(err) != store.FaultBackendBusy {
		t.Fatalf("expired policy: %v", err)
	}
}

func TestLowerNativeCapacityWaitsForHigherNumberedSlots(t *testing.T) {
	b, root := newStubProcessBackend(t)
	b.admissionFile = filepath.Join(root, "policy")
	dir := filepath.Join(root, "job")
	os.Mkdir(dir, 0700)
	policy := ProcessAdmission{Directory: filepath.Join(root, "slots"), Capacity: 1}
	os.MkdirAll(filepath.Join(policy.Directory, "2"), 0700)
	data, _ := json.Marshal(policy)
	os.WriteFile(b.admissionFile, data, 0600)
	spec := processSpec()
	if _, err := b.reserveSlot(dir, &spec); Fault(err) != store.FaultBackendBusy {
		t.Fatalf("higher slot did not block admission: %v", err)
	}
}
