# The typewriter and its toolbox. `make app` is the one-line answer for
# non-Go people: it produces a double-clickable ayfor.app.

.PHONY: build app cli test clean

VERSION ?= 0.2.0

build:
	go build -o ayfor ./cmd/ayfor

cli:
	go build -o strike ./cmd/strike

# Requires the same packager version as releases:
# go install fyne.io/tools/cmd/fyne@v1.7.2
app:
	fyne package --os darwin --src ./cmd/ayfor --name ayfor --app-id io.ayfor.app --icon $(CURDIR)/assets/icon.png --app-version $(VERSION)
	@if [ -d cmd/ayfor/ayfor.app ]; then mv cmd/ayfor/ayfor.app .; fi
	@test -d ayfor.app || { echo "make app: ayfor.app was not produced"; exit 1; }

test:
	go test -race ./...

clean:
	rm -rf ayfor strike ayfor.app
