.PHONY: build test vsix

build:
	npm run build

test:
	npm test
	go test ./...

vsix: build
	mkdir -p dist
	cp LICENSE packages/vscode/LICENSE
	cp THIRD_PARTY_NOTICES.md packages/vscode/THIRD_PARTY_NOTICES.md
	cd packages/vscode && npm exec --yes --package=@vscode/vsce@4.0.0 -- vsce package --no-dependencies --allow-missing-repository --no-rewrite-relative-links --out ../../dist/brote.vsix

.PHONY: test-vscode
test-vscode: vsix
	node packages/vscode/test/host/run.cjs
