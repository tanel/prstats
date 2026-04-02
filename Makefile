build:
	go build -o prstats .

run: build
	./prstats $(ARGS) $(REPO)

lint:
	golangci-lint run

test:
	go test ./...

validations:
