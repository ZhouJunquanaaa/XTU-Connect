# XTU-Connect Makefile
# make build   —— 全平台编译（等同 ./scripts/build.sh）
# make test    —— 运行单元测试
# make clean   —— 清理构建产物

.PHONY: build test vet clean fmt

build:
	./scripts/build.sh

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf dist
