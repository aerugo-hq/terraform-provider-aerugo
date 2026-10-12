# On Windows without make, run the commands after each colon in a terminal.
default: build

build:
	go build -v ./...

install:
	go install -v ./...

lint:
	go vet ./...

test:
	go test -v -count=1 ./...

# Against a running Aerugo (make up in the platform's repository); see the README for the AERUGO_* settings.
testacc:
	TF_ACC=1 go test -v -count=1 -timeout 30m ./...

.PHONY: default build install lint test testacc
