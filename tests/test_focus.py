"""What a key press actually puts on the socket.

Every signal around this call can report success while the screen does not
move, which is how herdr#3760 cost a day: `agent.focus` answered `ok`, the
server logged `workspace focused`, and the viewport stayed where it was. The
method name is therefore load-bearing in a way no other call here is, and
nothing above this level can catch it being wrong -- `test_daemon` fakes
`focus_agent` whole, so it would pass just as happily against the broken call.

So these drive a real unix socket and assert on the bytes.
"""

import json
import socket
import threading
from pathlib import Path

import pytest

from herdrkeys import herdr

# Bound relative, from inside the tmp dir: an AF_UNIX path is capped near 104
# bytes and pytest's absolute tmp paths overrun it on macOS.
SOCKET_NAME = Path("herdr.sock")


class FakeHerdr:
    """A socket that records requests and answers them however the test says."""

    def __init__(self, path, replies=None):
        self.requests = []
        self.replies = replies or {}
        self._sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self._sock.bind(str(path))
        self._sock.listen(8)
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def _serve(self):
        while True:
            try:
                conn, _ = self._sock.accept()
            except OSError:
                return
            with conn:
                buffer = b""
                while b"\n" not in buffer:
                    chunk = conn.recv(65536)
                    if not chunk:
                        break
                    buffer += chunk
                if b"\n" not in buffer:
                    continue
                request = json.loads(buffer.split(b"\n", 1)[0])
                self.requests.append(request)
                reply = self.replies.get(request["method"], {"result": {}})
                conn.sendall((json.dumps({"id": request["id"], **reply}) + "\n").encode())

    @property
    def methods(self):
        return [request["method"] for request in self.requests]

    def close(self):
        self._sock.close()


@pytest.fixture
def fake(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    server = FakeHerdr(SOCKET_NAME)
    yield server
    server.close()


def test_focus_moves_the_viewport_with_pane_focus(fake):
    # The whole point. `agent.focus` stopped reaching a surface-active client in
    # herdr 0.9.0, so focusing through it alone moves nothing on screen.
    herdr.focus_agent(SOCKET_NAME, "w2:p1")

    assert "pane.focus" in fake.methods, "pane.focus is what moves the viewport"
    assert fake.requests[0]["method"] == "pane.focus", "and it goes first"
    assert fake.requests[0]["params"] == {"pane_id": "w2:p1"}


def test_focus_also_marks_the_agent_seen(fake):
    # Going through the agent surface is what collapses `done` back to `idle`.
    herdr.focus_agent(SOCKET_NAME, "w2:p1")

    assert fake.methods == ["pane.focus", "agent.focus"]
    assert fake.requests[1]["params"] == {"target": "w2:p1"}


def test_a_departed_agent_does_not_cost_us_the_navigation(fake):
    # A pane outlives the agents that occupy it, so the frame can name a pane
    # whose agent has since gone. The viewport must still move.
    fake.replies["agent.focus"] = {
        "error": {"code": "agent_not_found", "message": "agent target null not found"}
    }

    herdr.focus_agent(SOCKET_NAME, "w2:p1")

    assert fake.methods == ["pane.focus", "agent.focus"]


def test_a_pane_that_has_closed_is_reported(fake):
    # Unlike a missing agent, a missing pane means the press did nothing at all,
    # and the daemon logs it rather than swallowing it.
    fake.replies["pane.focus"] = {
        "error": {"code": "pane_not_found", "message": "pane w9:p1 not found"}
    }

    with pytest.raises(herdr.HerdrError):
        herdr.focus_agent(SOCKET_NAME, "w9:p1")
