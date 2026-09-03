"""Enable the second USB serial channel.

The console channel stays on so the REPL remains reachable for development, and
the CIRCUITPY drive stays mounted so deploying is a file copy.

USB HID is deliberately left enabled even though herdrkeys never sends a
keystroke: disabling it buys nothing here (the risk of stray keystrokes comes
from code.py, which sends none) and leaving it on means restoring the board's
old keyboard layers later needs no boot.py change and no reset.

Changes to this file only take effect after the board is power-cycled.
"""

import usb_cdc

usb_cdc.enable(console=True, data=True)
