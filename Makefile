GATE_BIN    ?= wick-gate
LAB_BIN     ?= lab
TAILWIND    ?= bin/tailwindcss
CSS_IN      := web/src/input.css
CSS_OUT     := web/public/css/app.css

ifeq ($(OS),Windows_NT)
GATE_BIN  := wick-gate.exe
LAB_BIN   := lab.exe
TAILWIND  := bin/tailwindcss.exe
endif

.PHONY: build build-gate build-lab generate css dev install install-gate clean

generate:
	templ generate ./...

css:
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --minify

build-gate:
	go build -o $(GATE_BIN) ./cmd/gate/

build-lab: generate css
	go build -o $(LAB_BIN) ./cmd/lab/

build: build-gate build-lab

dev: generate
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT)
	go run ./cmd/lab/ server

install-gate:
	go install ./cmd/gate/

install: generate css install-gate
	go install ./cmd/lab/

clean:
	rm -f $(LAB_BIN) $(GATE_BIN)
