# 18. The git key wears its forge's logo

Date: 2026-11-05

## Status

Accepted

Takes up the feature row that ADR 0016 left "until it is considered on its
own", for the git key only. The Keybow, the frame's codes and the TUI are
unchanged.

## Context

On the CN001 the git key is a flat violet: bright while a press looks, mid when
the focused agent's directory has a page, dim when it has none. The colour says
whether there is somewhere to go, but not where. The LCD can draw, and the key
could show the **forge** a press will land on.

What forge a page is on can be told only from its host, and a host name is
certain only for the forges' own public hosts. `gitlab.mycorp.com` is probably
GitLab, and `git.mycorp.com` could be anything.

## Decision

**The forge is data beside the frame, like a project colour.** The daemon works
it out from the same `.git/config` read that decides `r` or `n`, on every frame,
without holding it back, and sends a forge name for slot 14 next to the frame.
The git key's codes stay as they are. The Keybow ignores the forge, because an
LED cannot draw a logo and a brand-coloured git LED would compete with the state
colours.

**Only exact public hosts are recognised:** `github.com`, `gitlab.com`,
`bitbucket.org`, `codeberg.org`. Any other host has a page but no forge, and the
key keeps today's violet. A wrong logo is worse than none. Guessing from words in
the host name, and a configured host-to-forge map, were both left for later.

**A known forge is drawn as a white logo on its brand colour.** The feature row
was never part of ADR 0014's "a saturated colour means what". It holds no
agents, and a brand colour is the quickest way to tell GitHub from GitLab at a
glance. GitLab's orange sits near `working` amber, but only in the feature row,
where amber never means `working`.

| state            | known forge                       | no known forge    |
| ---------------- | --------------------------------- | ----------------- |
| page (`r`)       | logo on brand colour              | violet, as before |
| looking (`g`)    | the same, with the focus border   | bright violet     |
| no page (`n`)    | --                                | dim violet        |

Looking stays steady. ADR 0012 still holds: motion means an agent wants you.

**Logos are coverage masks baked into the source.** `scripts/bake_logos.py`
rasterises each forge's SVG (from Simple Icons, CC0, kept in `assets/logos/`)
into a 56-pixel-square mask, centred in the key and clear of the focus border.
The result is checked in as compressed bytes in `logos.py`. Each byte says how
much of its pixel is logo, so at draw time white is mixed into the brand colour
by that much, and the edges come out smooth. A test fails if the checked-in
module and the SVGs disagree. Two other approaches were considered and
rejected:

- Drawing the logos from strokes and dots like the marks (ADR 0016). The
  Octocat and the tanuki do not survive being turned into a few shapes.
- Rasterising the SVGs on the host at run time. It needs the same rasteriser,
  run on every start for four images that never change. Baking runs it once,
  at development time, and keeps it out of the shipped path.

The masks keep ADR 0015 intact (no dependencies, no new `ctypes`) and stay
testable anywhere.

## Consequences

The git key now tells you where a press will go before you press it, but only
on the CN001 and only for the four public forges. Self-hosted GitLab and GitHub
Enterprise look exactly as they did.

Adding a forge means adding a host, a brand colour and an SVG, then rerunning
the script. The masks are generated files in the tree, so a logo that changes
upstream changes here only when someone updates its SVG and reruns the script.

The panel turns every image 90 degrees clockwise (ADR 0016), so the masks are
turned anticlockwise before encoding, like the marks.
