package instance

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOneDaemonAtATime(t *testing.T) {
	dir := t.TempDir()
	me := Info{PID: os.Getpid(), Version: "0.2.0", Path: "/a/herdrkeys", Started: time.Now()}
	lock, err := Acquire(dir, me)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, me); !errors.Is(err, ErrHeld) {
		t.Fatalf("a second daemon got the lock: %v", err)
	}
	info, running := Running(dir)
	if !running || info.PID != me.PID || info.Version != "0.2.0" {
		t.Fatalf("%+v %v", info, running)
	}
	lock.Release()
	if _, running := Running(dir); running {
		t.Fatal("still held after release")
	}
	again, err := Acquire(dir, me)
	if err != nil {
		t.Fatalf("not free again: %v", err)
	}
	again.Release()
}

func TestADifferentBuildIsNotTheSame(t *testing.T) {
	a := Info{Version: "0.2.0", Path: "/plugins/x/1/bin/herdrkeys"}
	if !a.Same(a) {
		t.Fatal("the same build")
	}
	if a.Same(Info{Version: "0.2.1", Path: a.Path}) || a.Same(Info{Version: "0.2.0", Path: "/plugins/x/2/bin/herdrkeys"}) {
		t.Fatal("every update installs to a fresh directory, and that is a different build")
	}
}

func TestStopAsksTheRunningDaemonToLetGo(t *testing.T) {
	// The real thing: a child process holds the lock and waits to be told.
	if os.Getenv("HERDRKEYS_INSTANCE_CHILD") != "" {
		lock, err := Acquire(os.Getenv("HERDRKEYS_INSTANCE_CHILD"), Info{PID: os.Getpid(), Version: "child"})
		if err != nil {
			os.Exit(2)
		}
		<-StopRequested()
		lock.Release()
		os.Exit(0)
	}
	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=TestStopAsksTheRunningDaemonToLetGo")
	child.Env = append(os.Environ(), "HERDRKEYS_INSTANCE_CHILD="+dir)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, running := Running(dir); running && info.Version == "child" {
			break
		}
		if time.Now().After(deadline) {
			child.Process.Kill()
			t.Fatal("the child never took the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // past setting up its stop listener
	stopped, err := Stop(dir, 5*time.Second)
	if err != nil || !stopped {
		child.Process.Kill()
		t.Fatalf("stopped %v, %v", stopped, err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("the child did not stop cleanly: %v", err)
	}
}
