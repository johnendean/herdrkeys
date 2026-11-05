# Forge logos

The marks the CN001's git key draws for a page on a known forge (ADR 0018).

From [Simple Icons](https://simpleicons.org) 13.0.0, which publishes them under
CC0. The logos remain their owners' trademarks and are used only to say which
service a link goes to.

They are not read at run time. `scripts/bake_logos.py` rasterises them into
`src/herdrkeys/logos.py`, which is what ships; rerun it after changing a file
here:

    python3 scripts/bake_logos.py
