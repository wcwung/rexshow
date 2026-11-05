PREFIX ?= $(HOME)/.local
SKILLS ?= $(HOME)/.agents/skills

.PHONY: build test install uninstall

build:
	go build -o rexshow .

test:
	go test ./...

install:
	go build -o $(PREFIX)/bin/rexshow .
	mkdir -p $(SKILLS)
	ln -sfn $(CURDIR)/skills/rexshow $(SKILLS)/rexshow

uninstall:
	rm -f $(PREFIX)/bin/rexshow
	rm -f $(SKILLS)/rexshow
