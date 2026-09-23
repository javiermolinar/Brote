.PHONY: build test vsix

build:
	npm run build

test:
	npm test
	go test ./...

vsix: build
	python3 scripts/release.py --host-only --output dist/releases
	python3 -c 'import pathlib, shutil; p = list(pathlib.Path("dist/releases").glob("brote-*-*.vsix")); assert len(p) == 1, p; shutil.copy2(p[0], "dist/brote.vsix")'

.PHONY: test-vscode
test-vscode: vsix
	node packages/vscode/test/host/run.cjs
