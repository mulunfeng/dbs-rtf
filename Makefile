.PHONY: build test clean run-cli run-server

build:
	go build -o bin/dbs-agent-cli ./cmd/cli/
	go build -o bin/dbs-agent-server ./cmd/server/

test:
	go test ./... -v -count=1

clean:
	rm -rf bin/

run-cli: build
	./bin/dbs-agent-cli list-plugins

run-server: build
	./bin/dbs-agent-server
