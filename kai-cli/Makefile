.PHONY: build test fmt clean

build:
	CGO_ENABLED=1 go build -o kai ./cmd/kai

test:
	CGO_ENABLED=1 go test ./...

fmt:
	gofmt -s -w .

clean:
	rm -f kai
	rm -rf testdata/repo/.kai

# Run the demo workflow
demo: build
	@echo "=== Initializing Kai in testdata/repo ==="
	cd testdata/repo && ../../kai init
	@echo ""
	@echo "=== Creating snapshot of main branch ==="
	cd testdata/repo && ../../kai snapshot main --repo .
	@echo ""
	@echo "=== Creating snapshot of feature branch ==="
	cd testdata/repo && ../../kai snapshot feature --repo .
	@echo ""
	@echo "=== Demo complete! Use the snapshot IDs above to analyze symbols and create changesets ==="
