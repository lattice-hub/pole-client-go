# Sidecar Session v1

## 目标

Thin SDK 只预先知道本机 Unix Domain Socket 路径，通过内部 gRPC 长连接接收
Sidecar 主动下发的业务 listener 地址。业务请求不经过该 gRPC 会话。

## Bootstrap 地址

- 默认 UDS：`/var/run/pole/sidecar/bootstrap.sock`
- 可选覆盖：`POLE_SIDECAR_SOCKET`
- Kubernetes Pod 通过共享 `emptyDir` 暴露该目录。

## 会话

Thin SDK 调用 `pole.sidecar.v1.SidecarSessionService/OpenSession` 建立 server-streaming
会话。该调用只用于建立连接和声明 SDK 信息，不携带端口查询条件。

Sidecar 必须把完整 `ListenerSnapshot` 作为首个 server message 主动下发。每个
会话只发送一次端口表；listener 在该 Sidecar 进程生命周期内保持不变。会话保持
打开，可供未来兼容事件扩展，并用于及时检测 Sidecar 退出。

Thin SDK 必须验证：

- 首条消息是 `listener_snapshot`；
- protocol 不重复且不是 `PROTOCOL_UNSPECIFIED`；
- port 位于 `1..65535`；
- HTTP、gRPC、Dubbo、Thrift 四种协议均存在。

验证成功后，SDK 原子安装不可变快照，并使用 `127.0.0.1:{port}` 构造本地业务
地址。SDK 不得内置或猜测 listener 端口。

## Listener 初始化

Sidecar 按以下顺序尝试绑定 loopback：

| 协议 | 默认端口 |
| --- | ---: |
| HTTP | 15001 |
| gRPC | 15002 |
| Dubbo | 15003 |
| Thrift | 15004 |

默认端口已占用时，Sidecar 回退到 `127.0.0.1:0`，由操作系统分配空闲端口。只有
全部 listener 就绪后才能接受并完成 `OpenSession` bootstrap。

## 失败与重连

- 启动时 UDS 不可用或 bootstrap 超时：SDK 有界指数退避重连，超过期限后初始化失败。
- 会话断开：SDK 立即使快照和旧连接池失效，新业务请求快速失败。
- 重连成功：Sidecar 在新会话首帧重新下发完整快照，SDK 原子恢复。
- SDK 不得绕过 Sidecar 直连真实服务。
