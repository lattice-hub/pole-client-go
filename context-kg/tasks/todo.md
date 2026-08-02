# Go Thin SDK

## 2026-08-02 Sidecar bootstrap 改造计划

- [x] 核对现有 SDK、权威契约与生成工具
- [x] vendor bootstrap protobuf 并生成 Go 绑定
- [x] 实现 UDS OpenSession、失效与重连
- [x] 迁移 TargetService 与业务元信息 API
- [x] 更新 README、依赖、CI 与测试
- [x] 运行 gofmt、test、race、vet

## 2026-08-02 Review

- `TargetEnvelope` 和旧 `x-pole-*` 字段均已删除；`TargetService` 仅保留 namespace/service。
- 业务协议地址仅由 `OpenSession` 首帧提供，SDK 使用 `atomic.Pointer` 原子安装/失效快照。
- UDS 断流立即清空快照；重连采用 50ms 到 1s 的指数退避，首次初始化默认在 10s 内失败。
- proto 来自 specification `v0.1.0-ALPHA.39`，不可变 tag、commit 与 checksum
  已写入 `contract/`。
- `gofmt`、contract checksum、普通测试、race 测试、`go vet` 和 diff 空白检查通过。

## 计划

- [x] 重新读取 Thin SDK 契约与一致性向量
- [x] 设计不可变 `TargetEnvelope` 公共 API
- [x] 实现 Unicode 规范化与 `Cc` 校验
- [x] 实现 `original_endpoint` 精确校验
- [x] 实现确定性 Header 编码与旧值覆盖
- [x] 补充单元测试、示例、README 和许可证
- [x] 运行 `gofmt`、`go test` 与 `go vet`
- [x] 复核写入范围与最终文件清单

## Review

- `TargetEnvelope` 字段私有，构造时复制、校验并规范化，公共 API 仅提供只读访问器。
- 原始字段先执行 Unicode `Cc` 检查，再执行 Unicode 空白 trim，确保首尾 `U+000A`、`U+0085` 也被拒绝。
- `Headers` 固定按契约顺序编码；`EncodeHTTPHeaders` 大小写不敏感地移除旧内部 Header 后写入新值。
- endpoint 测试覆盖合法 host、IPv4、IPv6、端口边界，以及缺端口、零端口、越界、非数字和裸 IPv6。
- `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...` 全部通过。
- `go list -m all` 仅包含当前模块，没有第三方依赖；目标目录未初始化 Git。

## 2026-07-31 零值不变量修复计划

- [x] 使用 CodeGraph 定位 `Headers`、`EncodeHTTPHeaders` 及其调用方
- [x] 让 Header 编码 API 返回并传播校验错误
- [x] 阻止零值和 nil `TargetEnvelope` 生成 Header
- [x] 移除构造流程中的位置索引字段映射
- [x] 拒绝 endpoint host 中的 Unicode 空白和 `/\[]@?#`
- [x] 更新单元测试、示例和 README
- [x] 运行 `gofmt`、`go test`、race test 与 `go vet`

## 2026-07-31 Review

- `Headers` 现返回 `([]Header, error)`，`EncodeHTTPHeaders` 现返回 `(http.Header, error)`；两者在编码前校验完整信封。
- 零值及 nil `TargetEnvelope` 都返回错误和 nil 结果，不再生成空 namespace/service Header，也不会修改输入 Header。
- 构造器通过字段目标指针完成命名映射，已移除 `normalized[6]` 等位置索引。
- endpoint host 继续允许合法 DNS、IPv4 与括号 IPv6，同时拒绝 Unicode 空白和 `/\[]@?#`。
- `gofmt -w envelope.go envelope_test.go example_test.go` 已执行。
- `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...` 全部通过。
- 当时 `go list -m all` 仅输出工程自身；正式接入后 module path 已迁移到
  `github.com/lattice-hub/pole-client-go`，仍保持标准库 only。
- 工程仍未初始化 Git，也未创建提交。

## 2026-07-31 正式 Thin SDK 契约接入

- [x] vendoring `thin-sdk-contract-v1.0.0` 契约资产与来源信息
- [x] 对齐 Unicode scalar、精确 White_Space 与 endpoint 规则
- [x] 实现 canonical UTF-8 Header 编码
- [x] 执行全部 SDK 与语言特定一致性向量
- [x] 校验 Sidecar receive 资产结构完整性
- [x] 更新 Go module、lattice-hub URL、README、CI 与治理文件
- [x] 运行本地验证并配置 Go 1.22/1.23 CI

## 2026-07-31 正式契约接入 Review

- vendored checksum 与 specification tag `thin-sdk-contract-v1.0.0` 一致。
- `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`
  与 `gofmt` 检查通过。
- CI 矩阵覆盖 Go 1.22/1.23；模块保持标准库 only，未发布版本标签。
- 最终审查修复 checksum/VERSION 假阳性；手动 release-check 与 CI 同步执行
  `gofmt`、race test 和 `go vet`。
