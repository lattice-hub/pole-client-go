package pole_test

import (
	"fmt"
	"net/http"

	pole "github.com/lattice-hub/pole-client-go"
)

func ExampleTargetService_EncodeHTTPHeaders() {
	target, err := pole.NewTargetService(" default ", " orders ")
	if err != nil {
		panic(err)
	}

	headers, err := target.EncodeHTTPHeaders(http.Header{
		pole.HeaderTargetNamespace: {"untrusted"},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(headers.Get(pole.HeaderTargetNamespace))
	fmt.Println(headers.Get(pole.HeaderTargetService))
	// Output:
	// default
	// orders
}
