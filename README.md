# Pole Go Thin SDK

`pole-client-go` 是 Pole Thin SDK 的 Go 核心包，提供与框架无关的
`TargetEnvelope v1` 构造、校验与 HTTP Header 编码。

当前版本不包含 `http.RoundTripper`、gRPC、Dubbo 或其他框架 adapter。

## 状态

当前工程实现 specification tag
[`thin-sdk-contract-v1.0.0`](https://github.com/lattice-hub/specification/tree/thin-sdk-contract-v1.0.0/thin-sdk/target-envelope/v1)，
vendored 资产与来源 commit 记录在 [`contract/`](contract/)。
Pole Sidecar 尚未实现对应的本地专用 listener，因此当前版本只提供契约核心，
不能宣称已经完成 Thin SDK 到 Sidecar 的端到端接入。

## 要求

- Go 1.22 或更高版本
- 仅使用 Go 标准库

## 安装

```bash
go get github.com/lattice-hub/pole-client-go
```

## 使用

```go
package main

import (
	"net/http"

	pole "github.com/lattice-hub/pole-client-go"
)

func main() {
	envelope, err := pole.NewTargetEnvelope(
		"default",
		"orders",
		pole.TargetEnvelopeOptions{
			Protocol:         "grpc",
			ServiceVersion:   "v1",
			Method:           "GetOrder",
			OriginalEndpoint: "orders.internal:8080",
		},
	)
	if err != nil {
		panic(err)
	}

	headers, err := envelope.EncodeHTTPHeaders(http.Header{
		"X-Request-ID": {"request-123"},
	})
	if err != nil {
		panic(err)
	}
	_ = headers
}
```

`EncodeHTTPHeaders` 返回输入 Header 的副本，保留非 Pole Header，并覆盖或删除
调用方提供的全部 TargetEnvelope 内部 Header。`Headers` 按契约固定顺序返回，
可用于需要确定性编码的传输实现。两个方法都会在编码前重新校验信封，并在
`TargetEnvelope` 是零值、nil 或状态无效时返回 error，绝不会生成缺失必填字段的
Header。

默认 Sidecar endpoint 为：

```go
pole.DefaultSidecarEndpoint // http://127.0.0.1:15001
```

## TargetEnvelope 语义

- 构造时拒绝非法 Unicode scalar，并按契约精确集合去除首尾空白。
- `namespace` 和 `service` 必填且不能为空。
- 任意字段都不允许包含 Unicode General Category `Cc` 控制字符。
- `original_endpoint` 只接受 `host:port` 或 `[ipv6]:port`。
- endpoint host 不允许 Unicode 空白或 `/\[]@?#%`。
- 端口必须是无前导零的十进制 `1..65535`。
- Header value 使用 canonical UTF-8 `%HH` 编码。
- 构造后的字段不可修改，只能通过只读访问器读取。

## 验证

```bash
gofmt -w .
go test ./...
go vet ./...
```

## License

BSD 3-Clause License，详见 [LICENSE](LICENSE)。
