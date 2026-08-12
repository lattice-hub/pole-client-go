# Pole Go Thin SDK

`pole-client-go` 是与框架无关的 Pole Thin SDK 核心包。它负责：

- 通过 `OpenControlSession` gRPC 协议与本 Pod 的 Sidecar 建立内部 bootstrap 会话；
- 从 Sidecar 首帧接收各业务协议的 loopback listener 地址；
- 注册本地 ingress 服务，并读取 Sidecar 回传的注册状态；
- 构造并注入经过校验、canonical percent 编码的 `TargetService` 元信息。

业务请求不经过 bootstrap gRPC 会话；它们仍使用原有 HTTP、gRPC、Dubbo 或
Thrift 协议访问 Sidecar listener，由 Sidecar 执行治理、服务发现和真实实例代理。

## 要求

- Go 1.22 或更高版本
- Sidecar 与业务进程共享 Unix Domain Socket：默认
  `/var/run/pole/sidecar/bootstrap.sock`

安装：

```bash
go get github.com/lattice-hub/pole-client-go
```

## Bootstrap 与 listener

`NewSidecarClient` 在其传入的 context 期限内等待有效首帧；默认上限为 10 秒。
Sidecar 未就绪时 SDK 以有界指数退避重试，超时后初始化失败，绝不绕过 Sidecar
直连真实服务。

```go
client, err := pole.NewSidecarClient(ctx, pole.BootstrapConfig{
	SDKVersion: "1.0.0",
})
if err != nil {
	return err
}
defer client.Close()

httpAddress, err := client.ListenerAddress(pole.ProtocolHTTP)
if err != nil {
	return err
}
// httpAddress 是 Sidecar 首帧返回的 127.0.0.1:port，不是服务实例地址。
```

`BootstrapConfig.SocketPath` 优先级最高；未设置时读取
`POLE_SIDECAR_SOCKET`，最后才使用默认 UDS 路径。SDK 不内置任何业务 listener
端口；Sidecar 会优先使用 HTTP `15001`、gRPC `15002`、Dubbo `15003`、Thrift
`15004`，冲突时可选择动态端口并通过首帧传递。

会话断流时 SDK 立即删除 listener 快照，`ListenerAddress` 返回
`ErrSidecarUnavailable`；后台重连成功并收到新的有效首帧后，地址原子恢复。框架
adapter 不应缓存该地址，也应在失效时自行废弃其持有的旧连接池。客户端首个控制事件
固定为 `ClientHello`；尚未注销的本地服务注册会在重连后重放。

```go
registration, err := client.RegisterLocalService("prod", "payments", pole.ProtocolHTTP, 8080)
if err != nil {
	return err
}
status, found := client.LocalServiceStatus(registration.RegistrationID)
_ = status
_ = found
if err := client.UnregisterLocalService(registration.RegistrationID); err != nil {
	return err
}
```

## TargetService

`TargetService v1` 只有 `namespace` 和 `service`。SDK 去除首尾契约空白、拒绝
Unicode `Cc` 控制字符和非法 UTF-8，然后以 canonical UTF-8 `%HH` 编码值。

```go
target, err := pole.NewTargetService("default", "orders")
if err != nil {
	return err
}

request, err := http.NewRequest(http.MethodGet, "http://"+httpAddress+"/orders/42", nil)
if err != nil {
	return err
}
request.Header, err = target.EncodeHTTPHeaders(request.Header)
if err != nil {
	return err
}
```

两个规范键始终由 SDK 覆盖调用方同名值：

```text
latticehub-target-namespace
latticehub-target-service
```

- HTTP 和 Thrift-over-HTTP：`EncodeHTTPHeaders`；Thrift 使用 Apache Thrift 官方
  HTTP Transport，不增加私有帧。
- gRPC：`EncodeGRPCMetadata`，将结果放入 outgoing metadata。
- Dubbo：`EncodeAttachments`，将结果写入请求 attachment。

Sidecar 必须在转发前再次校验这两个字段并删除它们。v1 的信任边界是 Kubernetes
Pod；不增加 session token 或逐请求签名。

## TrafficContext v1

`TrafficContext` 统一携带可选的 `campaign`、`lane` 与 `bucket`（`0..9999`）流量标签；
它不使用 `trace-id` 关联灰度。`NewTrafficContext` 校验标签后创建值，
`AttachTrafficContext` 返回派生的 `context.Context`，调用方保留父 context 即可在 scope
结束后恢复；`ResetTrafficContext` 创建不含标签并清理 OTel 流量成员的派生 context。
Go 的 `context.Context` 可直接与 OTel context 链共存：`AttachTrafficContext` 同时更新标准 OTel Baggage，
`CurrentTrafficContext` 在私有值不存在时从合法的 OTel Baggage 恢复标签，因此标准 W3C
Baggage Propagator 可直接完成跨进程传播。OTel Baggage 成员通过 `NewMemberRaw` 接收领域原值，
空格、Unicode 和字面 `%HH` 只会在 wire 序列化时编码一次。

`ExtractTrafficContext` 只解码 `baggage`，不会改变当前 context；
`InjectTrafficContext` 的显式参数优先，否则读取传入 context 的当前值。使用
`EncodeHTTPHeadersWithTrafficContext`、`EncodeGRPCMetadataWithTrafficContext` 或
`EncodeAttachmentsWithTrafficContext` 时，TargetService 与 TrafficContext 在同一个出站
装配点写入。前者会被本地 Sidecar 删除，W3C Baggage 会继续转发到真实服务。

```go
lane := "gray"
traffic, err := pole.NewTrafficContext(pole.TrafficContextInput{Lane: &lane})
if err != nil {
	return err
}
requestContext, err := pole.AttachTrafficContext(ctx, traffic)
if err != nil {
	return err
}
request.Header, err = target.EncodeHTTPHeadersWithTrafficContext(requestContext, request.Header, nil)
```

自动框架 hook、自动 OTel propagator 安装不在本次核心 SDK 范围内；框架 adapter 应在入口
extract 后按请求生命周期 attach，并在出站调用时使用上述统一编码 API。完整 wire 契约位于
`contract/traffic-context/v1/`。

## 契约资产

`contract/` vendor 了 `TargetService v1`、`TrafficContext v1`、Sidecar session 文档以及官方
`api/v1/sidecar/bootstrap.proto`。`internal/bootstrapv1/` 中的 Go 文件由
`protoc-gen-go` 和 `protoc-gen-go-grpc` 生成，不应手改。资产校验和位于
`contract/SHA256SUMS`。

当前 source specification 工作树中的 bootstrap 与 TargetService 资产尚未进入
已发布 tag；精确来源状态记录在 `contract/VERSION`。因此本 SDK 改造在本地验证
完成前不应作为已发布的稳定兼容性承诺。

## 验证

```bash
test -z "$(gofmt -l .)"
cd contract && sha256sum --check SHA256SUMS
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

## License

BSD 3-Clause License，详见 [LICENSE](LICENSE)。
