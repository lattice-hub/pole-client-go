# Go Thin SDK

## 2026-08-12 TrafficContext delivery audit

- [x] 重新核对权威契约、来源记录与当前未提交差异
- [x] 审查传播实现、公开 API 与出站 carrier 集成，修复明确问题
- [x] 执行格式化、单元、竞态、vet、契约校验与 diff 空白检查
- [x] 复核变更范围、风险与可提交边界

### Review

- 以已合入 Specification `develop` 的不可变提交
  `67b101bb6e3906b4337affefd33ef778cec692b3` 更新 `contract/VERSION`；当前 SDK
  仍未发布，故保留 `source_state=unreleased-develop`。
- 复核 `TrafficContext` 的 native `context.Context` 与 OTel Baggage bridge、canonical
  编解码和 conformance diagnostic；HTTP、gRPC、Dubbo 三个 TargetService 公共出站
  装配点均走同一个 `InjectTrafficContext` 路径。
- 修正 HTTP Header / gRPC Metadata 的 Baggage 键大小写边界：现在会收集并删除所有
  大小写变体，再只写一个规范 `baggage` carrier，避免旧保留成员绕过清理。
- `gofmt`、`go mod tidy`、普通/竞态测试、`go vet`、内外层契约 checksum 和
  `git diff --check` 全部通过；仓库无可用的 context-kg lint 脚本。
- 可提交范围仅包含 TrafficContext 运行时、契约资产、OTel 依赖、TargetService 三类
  carrier 集成、README 和本任务记录；未暂存、未提交、未切分支、未推送。

## 2026-08-06 TrafficContext v1

- [x] 核对 TargetService 编码入口与契约资产
- [x] 先写 TrafficContext 传播与作用域测试
- [x] 实现 `context.Context` 原生存储和 baggage 编解码
- [x] 接入 TargetService metadata 公共编码路径
- [x] 复制 v1 契约、更新 README 并完成全量验证
- [x] 复审 W3C OWS、外部空值与输入限制兼容性
- [x] 二轮复审：空上下文、大小写与 raw OWS 语义
- [x] 同步新版 conformance 空上下文清理向量与校验和
- [x] 注入清理全部精确小写保留前缀，extract 保持未知字段拒绝
- [x] 接入标准 OTel Baggage 存储与 private-value 缺失回退
- [x] 修正 OTel Raw 值编码并覆盖空格、中文和字面 `%HH`
- [x] 执行全部 TrafficContext conformance 向量并校验 diagnostic

### Review

- `TrafficContext` 使用可选 campaign/lane/bucket，Go 以派生 `context.Context` 形成
  scope；显式上下文优先于 current，父 context 或 `ResetTrafficContext` 恢复/清空状态。
- Baggage 保留外部成员、覆盖旧保留成员，严格校验 version、重复、未知键、property、
  canonical UTF-8 `%HH` 与成员/字节上限；无成员时返回 nil 供调用方删除 carrier。
- Baggage parser 按 W3C 支持 SP/HTAB OWS、外部空值、合法 property，并拒绝非法 token/
  baggage-octet；多 Header 按逗号合并后在输入阶段限制 8192 bytes。
- 二轮复审后，空 TrafficContext 与 version-only carrier 合法；注入仅清理旧保留成员，
  保留前缀判断大小写敏感，foreign raw 仅去除 member 的外层 OWS。
- TargetService 与 TrafficContext 分别在 HTTP、gRPC 和 Dubbo 的同一出站编码点写入，
  前者内部键仍覆盖，Baggage 保持下游可见。
- 已执行 `gofmt`、contract checksum、`go test -count=1 ./...`、
  `go test -race -count=1 ./...`、`go vet ./...` 和 `git diff --check`。
- 三轮复审后，`AttachTrafficContext` 会保留外部 OTel Baggage 并删除全部精确小写
  `latticehub.traffic.*` 成员后写入当前标签；私有 `context.Value` 缺失时，`CurrentTrafficContext`
  从合法 OTel Baggage 恢复，`ResetTrafficContext` 也会清除 OTel 流量成员。注入路径清理未知
  保留成员，但 extract 仍拒绝它们。
- 四轮复审后，OTel 成员统一使用 `baggage.NewMemberRaw` 接收领域原值，避免空格、Unicode
  和字面 `%HH` 被误解为预编码内容；conformance 测试完整执行 valid、sidecar receive valid/
  invalid，并通过 `TrafficContextError.Diagnostic` 精确比对规范诊断码。

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
- proto 来自 specification `develop` 提交 `776f590d1474c51847af75b44522953874097e55`，由 checksum
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

## 2026-08-06 OpenControlSession 迁移

- [x] 核对 `OpenControlSession` 权威契约与旧会话边界
- [x] 更新 vendored proto 与 Go 生成代码
- [x] 实现注册 API、状态处理与重连重放
- [x] 更新 README 和单元/UDS 集成测试
- [x] 运行 Go 验证并提交本地变更

### Review

- 契约引用 specification `develop` 集成提交 `2642bc29c0a512f4da84ec4eb862b1e1ceee9833`。
- `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...` 与
  vendored checksum 均通过。
