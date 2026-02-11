build:
	go build -o prstats .

run: build
	./prstats $(ARGS) $(REPO)
