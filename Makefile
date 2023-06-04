.PHONY: go_lint
go_lint:
	golangci-lint run ./...

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -buildvcs=false -o rollouts-plugin-trafficrouter-contour ./
