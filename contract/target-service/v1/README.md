# TargetService v1

## 目标

Thin SDK 将业务客户端的连接地址改为 Sidecar 对应协议 listener，并在原业务请求
中注入权威目标服务身份。Sidecar 使用该身份执行发现、治理和真实实例选址。

## 身份

TargetService v1 只有两个必填字段：

| 字段 | 含义 |
| --- | --- |
| `namespace` | Pole 命名空间 |
| `service` | Pole 运行时服务名 |

protocol 由 listener 唯一确定；method、path、Dubbo group/version 等由 Sidecar
从业务协议解析，不在 TargetService 中重复传输。

## 元信息键

| 键 | 字段 |
| --- | --- |
| `latticehub-target-namespace` | `namespace` |
| `latticehub-target-service` | `service` |

- HTTP：请求 Header。
- gRPC：request Metadata。
- Dubbo：request Attachment。
- Thrift：Apache Thrift 官方 HTTP Transport 的请求 Header，payload 继续使用
  Binary 或 Compact Protocol；不引入私有帧，不以 Java 尚未支持的 THeader
  作为跨语言基线。

Thin SDK 合并业务元信息时必须删除同名旧值，再写入规范值。Sidecar 必须要求两个
字段各出现一次，使用后删除，不得转发给真实服务。

## 规范化与编码

1. 输入必须是 Unicode scalar value 序列，拒绝孤立 UTF-16 surrogate。
2. 存在 Unicode General Category `Cc` 控制字符时拒绝。
3. 去除首尾 Unicode 15.1 `White_Space`；规范化后不能为空。
4. 将 UTF-8 字节编码为 ASCII：`0x20..0x7E` 中除 `%`、`,` 外原样保留，其他
   字节以及 `%`、`,` 编码为大写 `%HH`。
5. Sidecar 只接受 canonical 编码；原始逗号、非法 `%HH`、非法 UTF-8 或重编码
   不一致均拒绝。

## 信任边界

v1 以 Kubernetes Pod 为信任边界，不增加 session token 或逐请求签名。SDK
校验不是安全边界，Sidecar 必须重复校验。该模型不防御已被攻陷的同 Pod 进程。
