package daemon

import (
	"context"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/state"
)

// streamed is one event, or the end of a stream, from the stream with id.
type streamed struct {
	id    int
	event state.Event
	err   error
}

// pressed is one key edge, or the end of a board, from the device it came
// from -- so an edge from a board since replaced is recognised and dropped.
type pressed struct {
	board  *Board
	device board.Device
	key    board.Key
	closed bool
}

// Run is the loop. It returns only when ctx is done -- having let every board
// go -- or with ErrRestart.
func (d *Daemon) Run(ctx context.Context) error {
	start := time.Now()
	clock := func() float64 { return time.Since(start).Seconds() }
	d.Log.Info("herdrkeys starting; socket=" + d.Herdr.Endpoint())
	defer d.Close()

	events := make(chan streamed, 256)
	keys := make(chan pressed, 256)
	watchingStream := 0
	watching := map[*Board]board.Device{}

	for {
		now := clock()
		d.Step(now)

		// Start reading anything newly connected. A read goroutine ends with
		// its connection, saying so, and the edge it sends then is how a
		// board that was unplugged is noticed.
		if d.Stream != nil && watchingStream != d.streamID {
			watchingStream = d.streamID
			go readStream(d.Stream, d.streamID, events)
		}
		for _, b := range d.Boards {
			if b.Device != nil && watching[b] != b.Device {
				watching[b] = b.Device
				go readKeys(b, b.Device, keys)
			}
		}

		wait := time.NewTimer(time.Duration(d.Timeout(now) * float64(time.Second)))
		select {
		case <-ctx.Done():
			wait.Stop()
			return nil
		case first := <-events:
			wait.Stop()
			d.takeEvents(first, events, clock())
		case first := <-keys:
			wait.Stop()
			if err := d.takeKeys(first, keys, clock()); err != nil {
				return err
			}
		case found := <-d.gitLookup: // nil, and so never ready, while nothing is looking
			wait.Stop()
			d.landGitLookup(found, clock())
		case <-wait.C:
		}
	}
}

func readStream(s Stream, id int, out chan<- streamed) {
	for {
		event, err := s.Next()
		out <- streamed{id: id, event: event, err: err}
		if err != nil {
			return
		}
	}
}

func readKeys(b *Board, dev board.Device, out chan<- pressed) {
	for key := range dev.Keys() {
		out <- pressed{board: b, device: dev, key: key}
	}
	out <- pressed{board: b, device: dev, closed: true}
}

// takeEvents folds the event that woke the loop and any others waiting.
func (d *Daemon) takeEvents(first streamed, more <-chan streamed, now float64) {
	var batch []state.Event
	take := func(s streamed) {
		if s.id != d.streamID || d.Stream == nil {
			return // from a stream already dropped
		}
		if s.err != nil {
			d.PumpHerdr(now, batch)
			batch = nil
			d.DropHerdr(now, s.err.Error())
			return
		}
		batch = append(batch, s.event)
	}
	take(first)
	for {
		select {
		case s := <-more:
			take(s)
			continue
		default:
		}
		break
	}
	d.PumpHerdr(now, batch)
}

// takeKeys handles the key edge that woke the loop and any others waiting.
func (d *Daemon) takeKeys(first pressed, more <-chan pressed, now float64) error {
	take := func(p pressed) error {
		if p.board.Device != p.device {
			return nil // from a board already dropped, or since replaced
		}
		if p.closed {
			reason := "gone"
			if err := p.device.Err(); err != nil {
				reason = err.Error()
			}
			d.DropBoard(p.board, now, reason)
			return nil
		}
		return d.HandleKey(p.board, p.key, now)
	}
	if err := take(first); err != nil {
		return err
	}
	for {
		select {
		case p := <-more:
			if err := take(p); err != nil {
				return err
			}
			continue
		default:
		}
		return nil
	}
}
