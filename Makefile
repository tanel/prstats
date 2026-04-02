build:
	go build -o prstats .

run: build
	./prstats $(ARGS) $(REPO)

install:
	go install .

lint:
	golangci-lint run

test:
	go test ./...

validations:
