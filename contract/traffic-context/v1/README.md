# TrafficContext v1

## 目标

`TrafficContext` 是 Pole 治理的统一领域上下文。完整运行时对象包含协议、目标调用、
来源地址、单调时钟 deadline 和解析缓存，不能直接在线序列化。v1 只传播全链路流量
标签；Trace 继续使用 W3C `traceparent` / `tracestate`，每跳目标服务继续使用
TargetService v1。

## OTel 与原生运行时

- 存在 OpenTelemetry 时，Thin SDK 将以下成员写入 OTel Baggage，由标准 W3C
  Baggage Propagator 完成跨进程 inject/extract，并由 OTel Context 承载进程内状态。
- 不存在 OpenTelemetry 时，Thin SDK 使用语言原生 Context Storage，但必须复用相同
  Baggage 编解码和业务协议 carrier。
- Java Agent 只负责自动安装 OTel-backed 或 native storage，不维护第二套 wire 语义，
  也不得重复 inject。

## 运行时 API 语义

各语言可以使用符合本语言习惯的命名，但必须提供等价能力：

- `current()`：读取当前调用链的 `TrafficContext`；未安装时返回空值，不创建隐式标签。
- `attach(context)`：安装上下文并返回可关闭的 scope/token；回调作用域型运行时可以提供
  等价的 `runWith(context, operation)`。退出 scope 或回调后必须严格恢复上一个上下文，
  支持嵌套且不得泄漏到无关请求。Node.js 的标准 OTel bridge 必须使用
  `context.with` 实现 `runWith`，不能把 SDK 私有存储冒充为 OTel active Context。
- `extract(carrier)`：只解码 TrafficContext 保留成员；框架入口 hook 负责在请求生命周期内
  attach，codec 本身不修改全局状态。
- `inject(carrier, explicit?)`：显式参数优先，否则读取 `current()`；覆盖旧保留成员并保留
  外部 Baggage。没有 TrafficContext 时只执行旧保留成员清理。
- TargetService 与 TrafficContext 必须在同一个出站请求装配点注入；前者供本地 Sidecar
  消费并删除，后者供 Provider Thin SDK 继续 extract 和传播。

OTel bridge 必须把 `TrafficContext` 作为 OTel Context 中的领域值或等价 Baggage 视图，
不能通过 `trace-id` 反查或关联灰度标签。没有 OTel 时，Java/Node.js/Python/C# 使用语言
原生的异步上下文设施；Go 使用显式 `context.Context`；C++ 默认使用显式 capture/attach
与线程局部 fallback，跨线程任务必须显式携带捕获值。

## W3C Baggage 成员

| 成员 | 类型 | 约束 |
| --- | --- | --- |
| `latticehub.traffic.version` | uint8 | 当前固定为 `1` |
| `latticehub.traffic.campaign` | string | 可选，UTF-8 最多 128 bytes |
| `latticehub.traffic.lane` | string | 可选，UTF-8 最多 128 bytes |
| `latticehub.traffic.bucket` | uint16 | 可选，十进制 `0..9999` |

JSON Schema 的 `maxLength` 按 Unicode 字符计数，不能单独表达 UTF-8 字节上限；
`x-latticehub-maxUtf8Bytes: 128` 与 conformance 的超限反例共同构成规范约束，SDK
必须按编码后的 UTF-8 字节数校验，不能只依赖通用 Schema validator。

只要存在任一流量标签，就必须存在一次 version。所有保留成员最多出现一次且不允许
Baggage property。未知 `latticehub.traffic.*` 成员、重复字段、未知 version、非法
UTF-8、控制字符、非 canonical 编码或越界 bucket 必须拒绝。

campaign/lane 的 canonical 编码使用 UTF-8：RFC 3986 unreserved
`ALPHA / DIGIT / "-" / "." / "_" / "~"` 原样保留，其他字节使用大写 `%HH`。
值不能为空、不能包含控制字符，也不能有首尾空白。version 和 bucket 使用无前导零的
十进制；数值 `0` 例外。

注入时，SDK 必须保留非 `latticehub.traffic.*` Baggage 成员，删除所有旧的保留
前缀成员，再按 version、campaign、lane、bucket 顺序写入规范值。结果遵守 W3C
Baggage 的语法上限 180 个成员；64 个成员和总长度 8192 bytes 是最低完整传播保证。
若注入后没有任何成员，SDK 必须删除 carrier，不能写入空 `baggage` 值。

## Carrier

- HTTP、Thrift-over-HTTP：`baggage` Header。
- gRPC、Triple：`baggage` Metadata。
- Dubbo：名为 `baggage` 的 request Attachment。

与只供本地 Sidecar 消费的 TargetService 不同，TrafficContext Baggage 必须继续转发
给真实服务，供 Provider 侧 Thin SDK extract 并传播到后续调用。

## 信任边界

Baggage 只提供传播格式，不提供身份认证或灰度授权。v1 延续 Thin SDK + Sidecar 的
Pod 信任边界：本地 Thin SDK 覆盖调用方预置的保留成员，Sidecar 重复校验。来自外部
信任边界的 TrafficContext 必须由入口网关或后续安全层清洗或重新签发。
