.PHONY: build build-proxy test clean run-cli run-server

# Cross-platform compatible build
ifeq ($(OS),Windows_NT)
	EXT = .exe
	RM = del /Q
else
	EXT =
	RM = rm -f
endif

build:
	go build -o bin/dbs-agent-cli$(EXT) ./cmd/cli/
	go build -o bin/dbs-agent-server$(EXT) ./cmd/server/

build-proxy:
	go build -o bin/mysql-proxy$(EXT) ./infra/mysql-proxy/

test:
	go test ./... -v -count=1

clean:
	-$(RM) bin/dbs-agent-cli$(EXT)
	-$(RM) bin/dbs-agent-server$(EXT)
	-$(RM) bin/mysql-proxy$(EXT)

run-cli: build
	./bin/dbs-agent-cli$(EXT) list-plugins

run-server: build
	./bin/dbs-agent-server$(EXT)
