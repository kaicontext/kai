.PHONY: build test fmt clean

build:
	go build -o ivcs ./cmd/ivcs

test:
	go test ./...

fmt:
	gofmt -s -w .

clean:
	rm -f ivcs
	rm -rf testdata/repo/.ivcs

# Run the demo workflow
demo: build
	@echo "=== Initializing IVCS in testdata/repo ==="
	cd testdata/repo && ../../ivcs init
	@echo ""
	@echo "=== Creating snapshot of main branch ==="
	cd testdata/repo && ../../ivcs snapshot main --repo .
	@echo ""
	@echo "=== Creating snapshot of feature branch ==="
	cd testdata/repo && ../../ivcs snapshot feature --repo .
	@echo ""
	@echo "=== Demo complete! Use the snapshot IDs above to analyze symbols and create changesets ==="
