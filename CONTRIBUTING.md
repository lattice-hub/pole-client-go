# 贡献指南

1. 从 `develop` 创建短生命周期分支。
2. 保持核心包标准库 only，不在本仓直接加入框架 adapter。
3. 契约行为必须先在 `lattice-hub/specification` 发布，再同步 vendored 资产。
4. 提交前运行 `gofmt -w .`、`go test -race -count=1 ./...`、
   `go vet ./...` 和 `cd contract && shasum -a 256 -c SHA256SUMS`。
5. Pull Request 需说明行为变化、契约版本和验证结果。
