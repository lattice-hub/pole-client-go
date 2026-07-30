package pole_test

import (
	"fmt"
	"net/http"

	pole "github.com/lattice-hub/pole-client-go"
)

func ExampleTargetEnvelope() {
	envelope, err := pole.NewTargetEnvelope(" default ", " orders ", pole.TargetEnvelopeOptions{
		Protocol:         "grpc",
		ServiceVersion:   "v1",
		Method:           "GetOrder",
		OriginalEndpoint: "orders.internal:8080",
	})
	if err != nil {
		panic(err)
	}

	headers, err := envelope.EncodeHTTPHeaders(http.Header{
		pole.HeaderNamespace: {"untrusted"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(envelope.Namespace(), envelope.Service())
	fmt.Println(headers.Get(pole.HeaderEnvelopeVersion))
	fmt.Println(headers.Get(pole.HeaderOriginalEndpoint))

	// Output:
	// default orders
	// 1
	// orders.internal:8080
}
