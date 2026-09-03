REPO    := $(shell pwd)
PYTHON  := $(REPO)/.venv/bin/python
DRIVE   := /Volumes/CIRCUITPY
PLIST   := $(HOME)/Library/LaunchAgents/dev.herdrkeys.plist

.PHONY: help venv test doctor tui run deploy install-agent uninstall-agent logs

help:
	@echo "make venv            create .venv and install herdrkeys"
	@echo "make test            run the test suite (no hardware needed)"
	@echo "make doctor          check herdr, the keypad, and the terminal app"
	@echo "make tui             run the daemon against a keypad drawn in the terminal"
	@echo "make run             run the daemon in the foreground"
	@echo "make deploy          copy device/ onto CIRCUITPY (power-cycle after)"
	@echo "make install-agent   install and start the launchd agent"
	@echo "make uninstall-agent stop and remove the launchd agent"
	@echo "make logs            tail the daemon log"

venv:
	python3 -m venv .venv
	$(REPO)/.venv/bin/pip install -q --upgrade pip
	$(REPO)/.venv/bin/pip install -q -e '.[dev]'

test:
	$(PYTHON) -m pytest -q

doctor:
	-$(PYTHON) -m herdrkeys doctor

tui:
	$(PYTHON) -m herdrkeys tui

run:
	$(PYTHON) -m herdrkeys run

deploy:
	@test -d $(DRIVE) || { echo "$(DRIVE) is not mounted -- plug the Keybow in"; exit 1; }
	cp device/boot.py $(DRIVE)/boot.py
	cp device/code.py $(DRIVE)/code.py
	@sync
	@echo "deployed. boot.py only takes effect after a power cycle:"
	@echo "  unplug the Keybow and plug it back in, then run 'make doctor'"

install-agent:
	@mkdir -p $(dir $(PLIST))
	@sed 's|__REPO__|$(REPO)|g' launchd/dev.herdrkeys.plist.template > $(PLIST)
	-launchctl bootout gui/$(shell id -u)/dev.herdrkeys 2>/dev/null
	launchctl bootstrap gui/$(shell id -u) $(PLIST)
	@echo "installed $(PLIST)"

uninstall-agent:
	-launchctl bootout gui/$(shell id -u)/dev.herdrkeys
	rm -f $(PLIST)

logs:
	tail -f $(REPO)/.venv/herdrkeys.log
