.PHONY: all deps build linux darwin clean

all: build

deps:
	go mod download

build:
	go build -o mybckchk .

linux:
	GOOS=linux go build -o mybckchk .

darwin:
	GOOS=darwin go build -o mybckchk .

clean:
	rm -f mybckchk
