.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin tests)"
	go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# Exit 1 means it found problems, which the broken example has on purpose.
demo:
	go run ./plugin/scripts examples/broken || test $$? -eq 1
	go run ./plugin/scripts --go '*' examples/fixed
