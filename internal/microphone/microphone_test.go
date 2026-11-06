package microphone

// The one microphone, decided on the host for every board (ADR 0017).
//
// A held chord is the worst thing herdrkeys can leave behind on a machine, so
// every way the microphone closes is a test of its own.

import (
	"slices"
	"testing"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
)

func setup() (*Microphone, *board.Fake, *board.Fake) {
	c, _ := chord.Parse("ctrl+win")
	return New(c), board.NewFake(), board.NewFake()
}

func want(t *testing.T, mic *Microphone, state model.Microphone) {
	t.Helper()
	if mic.State() != state {
		t.Fatalf("microphone is %q, want %q", mic.State(), state)
	}
}

func TestHoldingTheKeyHoldsTheChordUntilItComesUp(t *testing.T) {
	mic, keybow, _ := setup()
	mic.Down(keybow, 0.0)
	if !keybow.Holding || !slices.Equal(keybow.Held.Names(), []string{"LEFT_CONTROL", "LEFT_GUI"}) {
		t.Fatalf("holding %v %v: the chord the host chose", keybow.Holding, keybow.Held)
	}
	want(t, mic, model.MicOpen)

	mic.Up(keybow, 2.0)
	if keybow.Holding {
		t.Fatal("a hold ends when your finger does")
	}
	want(t, mic, model.MicClosed)
}

func TestATapLatchesTheMicrophoneOpenAndAnotherTapEndsIt(t *testing.T) {
	mic, keybow, _ := setup()
	mic.Down(keybow, 0.0)
	mic.Up(keybow, LatchTapSeconds/2)
	if !keybow.Holding {
		t.Fatal("latched")
	}
	want(t, mic, model.MicLatched)

	mic.Down(keybow, 10.0)
	want(t, mic, model.MicClosed)
	mic.Up(keybow, 10.1)
	if keybow.Holding {
		t.Fatal("the finger that closed it must not reopen it")
	}
}

func TestALatchDoesNotOutliveItsTimeout(t *testing.T) {
	mic, keybow, _ := setup()
	mic.Down(keybow, 0.0)
	mic.Up(keybow, 0.1)
	mic.Tick(0.1 + LatchTimeout - 1)
	if !keybow.Holding {
		t.Fatal("still inside the timeout")
	}
	if d, ok := mic.NextChange(0.1 + LatchTimeout - 1); !ok || d < 0.99 || d > 1.01 {
		t.Fatalf("next change = %v, %v: the loop wakes for it", d, ok)
	}
	mic.Tick(0.1 + LatchTimeout + 1)
	if keybow.Holding {
		t.Fatal("timed out")
	}
	want(t, mic, model.MicClosed)
}

func TestALongHoldIsNotCutOffByTheLatchTimeout(t *testing.T) {
	mic, keybow, _ := setup()
	mic.Down(keybow, 0.0)
	mic.Tick(LatchTimeout + 1)
	if !keybow.Holding {
		t.Fatal("a finger on the key is its own timeout")
	}
}

func TestReturnTypesOnTheBoardItWasPressedOn(t *testing.T) {
	mic, keybow, host := setup()
	mic.PressReturn(host)
	if host.Returns != 1 || keybow.Returns != 0 {
		t.Fatalf("returns %d / %d", host.Returns, keybow.Returns)
	}
}

func TestReturnWhileTheMicrophoneIsOpenClosesItAndTypesNothing(t *testing.T) {
	// Flow inserts what it heard some time after it stops listening, so a
	// Return now would send the prompt without it.
	mic, keybow, _ := setup()
	mic.Down(keybow, 0.0)
	mic.PressReturn(keybow)
	if keybow.Holding || keybow.Returns != 0 {
		t.Fatal("closed, and nothing typed")
	}
	want(t, mic, model.MicClosed)
	mic.PressReturn(keybow)
	if keybow.Returns != 1 {
		t.Fatal("the next press sends")
	}
}

func TestReturnOnOneBoardClosesAMicrophoneHeldByTheOther(t *testing.T) {
	mic, keybow, host := setup()
	mic.Down(keybow, 0.0)
	mic.Up(keybow, 0.1) // latched on the Keybow
	mic.PressReturn(host)
	if keybow.Holding {
		t.Fatal("there is one microphone, whoever holds it")
	}
	if host.Returns != 0 || keybow.Returns != 0 {
		t.Fatal("and nothing is typed")
	}
}

func TestOnlyOneBoardEverHoldsTheChord(t *testing.T) {
	mic, keybow, host := setup()
	mic.Down(keybow, 0.0)
	mic.Down(host, 1.0)
	mic.Up(host, 1.1)
	if !keybow.Holding || host.Holding {
		t.Fatal("a second holder is a second thing to forget to let go")
	}
	want(t, mic, model.MicOpen)
	mic.Up(keybow, 3.0)
	if keybow.Holding || host.Holding {
		t.Fatal("let go")
	}
}

func TestLosingTheBoardThatHoldsItClosesTheMicrophone(t *testing.T) {
	mic, keybow, host := setup()
	mic.Down(host, 0.0)
	mic.Up(host, 0.1)
	mic.Lost(keybow)
	if !host.Holding {
		t.Fatal("a board that held nothing has nothing to give back")
	}
	mic.Lost(host)
	if host.Holding {
		t.Fatal("lost, so let go")
	}
	want(t, mic, model.MicClosed)
}

func TestClosingWithNothingHeldDoesNothing(t *testing.T) {
	mic, keybow, _ := setup()
	if err := mic.Close(); err != nil {
		t.Fatal(err)
	}
	if keybow.Holding {
		t.Fatal("nothing was held")
	}
}
